import {Events, WML} from "@wailsio/runtime";
import {BatteryService} from "../bindings/felicity-battery-watch";

// Wire up data-wml-openURL links once the DOM is ready.
WML.Enable();

const POLL_INTERVAL_S = 60;

const scanView = document.getElementById('scan-view');
const detailView = document.getElementById('detail-view');
const scanBtn = document.getElementById('scan-btn');
const scanBtnLabel = document.getElementById('scan-btn-label');
const scanStatus = document.getElementById('scan-status');
const deviceList = document.getElementById('device-list');
const emptyState = document.getElementById('empty-state');
const errorBanner = document.getElementById('error-banner');

const modeBLEBtn = document.getElementById('mode-ble');
const modeTCPBtn = document.getElementById('mode-tcp');
const tcpFields = document.getElementById('tcp-fields');
const tcpHostInput = document.getElementById('tcp-host');
const tcpPortInput = document.getElementById('tcp-port');
const transportHint = document.getElementById('transport-hint');

const detailName = document.getElementById('detail-name');
const detailSn = document.getElementById('detail-sn');
const socValue = document.getElementById('soc-value');
const socBar = document.getElementById('soc-bar');
const kwhValue = document.getElementById('kwh-value');
const updatedLabel = document.getElementById('updated-label');
const voltageValue = document.getElementById('voltage-value');
const currentSign = document.getElementById('current-sign');
const currentValue = document.getElementById('current-value');
const capacityValue = document.getElementById('capacity-value');
const nextPollValue = document.getElementById('next-poll-value');

const toast = document.getElementById('toast');
const toastLabel = document.getElementById('toast-label');
const toastMsg = document.getElementById('toast-msg');

let toastTimer;
let scanInFlight = false;
let connectingId = null;
let connectedId = null;
let lastReadingTs = null;
let pollCountdown = POLL_INTERVAL_S;
let countdownTimer = null;
let autoRetryTimer = null;
const AUTO_RETRY_MS = 3000;

// 'ble' | 'tcp' — mirrored from the backend, which owns the real link.
let transport = 'tcp';

const BLE_HINT = 'Bluetooth LE — the battery advertises as <strong>F&lt;DevSN&gt;</strong>.';

function tcpHint(host, port) {
  const target = escapeHtml(`${host || '192.168.155.1'}:${port || 53970}`);
  const local = host === '127.0.0.1' || host === 'localhost' || (host || '').startsWith('::1');
  const how = local
    ? 'via the phone/adb relay — see go-fsolar/README.md.'
    : 'this PC must be joined to the battery\u2019s WiFi hotspot. Each reading uses a fresh connect/read/disconnect to handle flaky links.';
  return `TCP <strong>${target}</strong> — ${how}`;
}

function endpointLabel() {
  const host = tcpHostInput.value.trim() || '192.168.155.1';
  const port = Number(tcpPortInput.value) || 53970;
  return `${host}:${port}`;
}

function setTransportUI(mode) {
  transport = mode === 'tcp' ? 'tcp' : 'ble';
  const isTCP = transport === 'tcp';
  modeBLEBtn.classList.toggle('is-active', !isTCP);
  modeTCPBtn.classList.toggle('is-active', isTCP);
  modeBLEBtn.setAttribute('aria-pressed', String(!isTCP));
  modeTCPBtn.setAttribute('aria-pressed', String(isTCP));
  tcpFields.hidden = !isTCP;
  deviceList.hidden = isTCP;
  scanBtnLabel.textContent = isTCP ? 'Connect' : 'Scan 10s';
  transportHint.innerHTML = isTCP
    ? tcpHint(tcpHostInput.value.trim(), Number(tcpPortInput.value))
    : BLE_HINT;
}

function clearAutoRetry() {
  if (autoRetryTimer) {
    clearTimeout(autoRetryTimer);
    autoRetryTimer = null;
  }
}

function scheduleAutoRetry() {
  clearAutoRetry();
  // Auto-retry is a Bluetooth-scan convenience only: hammering a dial on a
  // failed TCP connect is unkind to the battery, which serves one client.
  if (transport === 'tcp' || scanInFlight || !scanView.hidden === false) return;
  autoRetryTimer = setTimeout(() => {
    if (!scanView.hidden && !scanInFlight) doScan();
  }, AUTO_RETRY_MS);
}

function showToast(label, message) {
  toastLabel.textContent = label;
  toastMsg.textContent = message;
  toast.classList.add('is-visible');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toast.classList.remove('is-visible'), 5000);
}

function showError(message) {
  if (!message) {
    errorBanner.hidden = true;
    errorBanner.textContent = '';
    return;
  }
  errorBanner.hidden = false;
  errorBanner.textContent = message;
}

function errorMessage(err) {
  if (!err) return 'Unknown error';
  if (typeof err === 'string') return err;
  if (err.message) return err.message;
  return String(err);
}

function rssiBars(rssi) {
  const v = Number(rssi) || 0;
  if (v > -50) return 4;
  if (v > -65) return 3;
  if (v > -80) return 2;
  return 1;
}

function fmt(n, digits = 1) {
  if (n === null || n === undefined || Number.isNaN(Number(n))) return '--';
  return Number(n).toFixed(digits);
}

function setSocFocus(on) {
  socFocus = !!on;
  document.body.classList.toggle('soc-focus', socFocus);
}

function showView(viewName) {
  const isDetail = viewName === 'detail';
  scanView.hidden = isDetail;
  detailView.hidden = !isDetail;
  if (!isDetail) {
    stopCountdown();
    setSocFocus(false);
  }
}

function renderDevices(devices) {
  if (!Array.isArray(devices)) devices = [];
  deviceList.innerHTML = '';
  if (devices.length === 0) {
    deviceList.appendChild(emptyState);
    return;
  }
  for (const d of devices) {
    const card = document.createElement('button');
    card.className = 'device-card';
    card.type = 'button';
    card.disabled = connectingId === d.id;
    const name = d.name || d.devSn || 'Unknown battery';
    const sub = d.devSn ? `DevSN ${d.devSn}` : (d.id || '');
    const bars = '▮'.repeat(rssiBars(d.rssi));
    card.innerHTML = `
      <div class="device-main">
        <div class="device-name">${escapeHtml(name)}${d.isFelicity ? '<span class="badge">Felicity</span>' : ''}</div>
        <div class="device-sub">${escapeHtml(sub)}</div>
      </div>
      <div class="device-side">
        <span class="device-rssi num" title="RSSI ${d.rssi} dBm">${bars} <small>${d.rssi}</small></span>
        <span class="device-id num">${escapeHtml(shortId(d.id))}</span>
      </div>`;
    card.addEventListener('click', () => connect(d.id, name, d.devSn));
    deviceList.appendChild(card);
  }
}

function shortId(id) {
  if (!id) return '';
  return id.length > 18 ? `${id.slice(0, 8)}…${id.slice(-6)}` : id;
}

function escapeHtml(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}

function startCountdown() {
  stopCountdown();
  pollCountdown = POLL_INTERVAL_S;
  nextPollValue.textContent = String(pollCountdown);
  countdownTimer = setInterval(() => {
    pollCountdown = Math.max(0, pollCountdown - 1);
    nextPollValue.textContent = String(pollCountdown);
    if (lastReadingTs) {
      const ago = Math.floor((Date.now() - lastReadingTs) / 1000);
      updatedLabel.textContent = `Updated ${ago}s ago`;
    }
  }, 1000);
}

function stopCountdown() {
  if (countdownTimer) {
    clearInterval(countdownTimer);
    countdownTimer = null;
  }
}

function renderCurrent(amps) {
  const v = Number(amps);
  currentValue.classList.remove('current-charge', 'current-discharge');
  if (!Number.isFinite(v)) {
    currentSign.textContent = '';
    currentValue.textContent = '--';
    return;
  }
  if (v > 0) {
    currentSign.textContent = '+';
    currentValue.textContent = fmt(v, 1);
    currentValue.classList.add('current-charge');
  } else if (v < 0) {
    currentSign.textContent = '\u2212'; // minus stays white
    currentValue.textContent = fmt(Math.abs(v), 1);
    currentValue.classList.add('current-discharge');
  } else {
    currentSign.textContent = '';
    currentValue.textContent = fmt(0, 1);
  }
}

function renderReading(r) {
  if (!r) return;
  socValue.textContent = fmt(r.soc, 0);
  const pct = Math.min(100, Math.max(0, Number(r.soc) || 0));
  socBar.style.width = `${pct}%`;
  kwhValue.textContent = fmt(r.remainingKWh, 2);
  voltageValue.textContent = fmt(r.voltage, 2);
  renderCurrent(r.current);
  capacityValue.textContent = fmt(r.capacityAh, 1);
  if (r.timestamp) {
    lastReadingTs = new Date(r.timestamp).getTime();
    if (Number.isNaN(lastReadingTs)) lastReadingTs = Date.now();
  } else {
    lastReadingTs = Date.now();
  }
  if (r.devSn) detailSn.textContent = `DevSN ${r.devSn}`;
  showError('');
  updatedLabel.textContent = 'Updated just now';
  pollCountdown = POLL_INTERVAL_S;
  if (countdownTimer) nextPollValue.textContent = String(pollCountdown);
}

async function doScan() {
  if (transport === 'tcp') return doConnectTCP();
  if (scanInFlight) return;
  scanInFlight = true;
  showError('');
  renderDevices([]);
  scanStatus.textContent = 'Scanning 10s for Felicity batteries…';
  scanBtn.classList.add('is-loading');
  scanBtn.disabled = true;
  try {
    const devices = await BatteryService.Scan(10);
    renderDevices(devices || []);
    const f = (devices || []).filter(d => d.isFelicity).length;
    scanStatus.textContent = devices?.length
      ? `Found ${devices.length} device(s) (${f} Felicity). Tap one to connect.`
      : 'Scan complete — no devices found. Retrying in 3s…';
    if (f === 0) scheduleAutoRetry();
  } catch (err) {
    console.error(err);
    showError(errorMessage(err));
    scanStatus.textContent = 'Scan failed. Retrying in 3s…';
    scheduleAutoRetry();
  } finally {
    scanInFlight = false;
    scanBtn.classList.remove('is-loading');
    scanBtn.disabled = false;
  }
}

function clearReadingFields() {
  socValue.textContent = '--';
  kwhValue.textContent = '--';
  voltageValue.textContent = '--';
  currentSign.textContent = '';
  currentValue.textContent = '--';
  currentValue.classList.remove('current-charge', 'current-discharge');
  capacityValue.textContent = '--';
  socBar.style.width = '0%';
  updatedLabel.textContent = 'Waiting for first reading…';
}

// switchTransport asks the backend to change transport, then mirrors it in
// the UI. Any live link is closed first — the battery serves one client.
async function switchTransport(mode) {
  if (mode === transport) return;
  clearAutoRetry();
  if (connectedId) {
    try {
      await BatteryService.Disconnect();
    } catch (err) {
      console.error(err);
    }
    connectedId = null;
    stopCountdown();
  }
  setTransportUI(mode);
  showView('scan');
  renderDevices([]);
  if (mode === 'tcp') {
    scanStatus.textContent = 'Set the host and port, then press Connect.';
    await applyTcpSettings();
  } else {
    scanStatus.textContent = 'Ready to scan.';
    doScan();
  }
}

// Remember the typed endpoint so it survives a transport switch and a restart
// of the view. Empty values are fine — the backend fills in its defaults.
async function applyTcpSettings() {
  try {
    await BatteryService.SetTransport(
      transport, tcpHostInput.value.trim(), Number(tcpPortInput.value) || 0);
  } catch (err) {
    console.error(err);
    showError(errorMessage(err));
  }
}

// WiFi hotspot / TCP watch: each reading does a fresh connect → request →
// disconnect cycle to tolerate flaky links, on a 60 s schedule.
async function doConnectTCP() {
  if (scanInFlight) return;
  scanInFlight = true;
  showError('');
  const host = tcpHostInput.value.trim();
  const port = Number(tcpPortInput.value) || 0;
  const label = `${host || '192.168.155.1'}:${port || 53970}`;
  scanStatus.textContent = `Connecting to ${label} (fresh link per reading)…`;
  scanBtn.classList.add('is-loading');
  scanBtn.disabled = true;
  // Cleared before the call: the backend may emit the first reading inside
  // ConnectTCP, so resetting after the await would erase it.
  clearReadingFields();
  try {
    await BatteryService.ConnectTCP(host, port);
    connectedId = label;
    detailName.textContent = `WiFi · ${label}`;
    detailSn.textContent = 'TCP watch: fresh connect/read/disconnect each poll';
    showView('detail');
    showToast('Watching', `${label} · reading every ${POLL_INTERVAL_S}s (flaky-link safe)`);
    startCountdown();
  } catch (err) {
    console.error(err);
    showError(`Connect failed: ${errorMessage(err)}`);
    scanStatus.textContent = 'Could not reach the battery. Check host/port (and the adb relay), then press Connect again.';
  } finally {
    scanInFlight = false;
    scanBtn.classList.remove('is-loading');
    scanBtn.disabled = false;
  }
}

async function connect(id, name, devSn) {
  if (!id || connectingId) return;
  connectingId = id;
  clearAutoRetry();
  showError('');
  scanStatus.textContent = `Connecting to ${name || id}…`;
  // Clear before Connect: the backend emits the first reading
  // synchronously inside Connect(), so resetting after the await
  // would erase that already-rendered SOC/kWh.
  clearReadingFields();
  try {
    await BatteryService.Connect(id);
    connectedId = id;
    detailName.textContent = name || id;
    detailSn.textContent = devSn ? `DevSN ${devSn}` : `ID ${id}`;
    showView('detail');
    showToast('Connected', `${name || id} · reading every ${POLL_INTERVAL_S}s`);
    startCountdown();
  } catch (err) {
    console.error(err);
    showError(`Connect failed: ${errorMessage(err)}`);
    scanStatus.textContent = 'Connection failed. Retrying in 3s…';
    scheduleAutoRetry();
  } finally {
    connectingId = null;
    renderDevicesSafe();
  }
}

let lastDevicesCache = [];
let socFocus = false;

function renderDevicesSafe() {
  if (lastDevicesCache.length) renderDevices(lastDevicesCache);
}

async function disconnect(silent = false) {
  try {
    await BatteryService.Disconnect();
  } catch (err) {
    console.error(err);
    if (!silent) showError(errorMessage(err));
  }
  connectedId = null;
  showView('scan');
  scanStatus.textContent = 'Disconnected.';
  if (!silent) showToast('Disconnected', 'Link closed.');
  clearAutoRetry();
  if (transport === 'tcp') {
    scanStatus.textContent = 'Disconnected. Press Connect to dial again.';
  } else {
    scanStatus.textContent = 'Disconnected. Scanning again…';
    doScan();
  }
}

// Backend push events (broadcast while polling / scanning).
Events.On('battery:devices', (evt) => {
  lastDevicesCache = evt?.data || [];
  if (!scanView.hidden) renderDevices(lastDevicesCache);
});

Events.On('battery:reading', (evt) => {
  renderReading(evt?.data);
});

Events.On('battery:connection', (evt) => {
  const c = evt?.data || {};
  if (c.connected) {
    if (c.error) {
      showError(c.error);
      showToast('Connected', c.error);
    }
  } else {
    stopCountdown();
    if (c.error) {
      showError(`Connection lost: ${c.error}`);
      showToast('Disconnected', c.error);
    }
    if (!detailView.hidden) {
      disconnectedFallback();
    } else {
      showView('scan');
    }
  }
});

function disconnectedFallback() {
  // Stay on detail view but clear live numbers, offer rescan via Back.
  socValue.textContent = '--';
  kwhValue.textContent = '--';
  socBar.style.width = '0%';
  updatedLabel.textContent = transport === 'tcp'
    ? 'Link closed — go Back, then Connect again.'
    : 'Link closed — go Back and rescan.';
  connectedId = null;
}

// Backend push events for transport changes (SetTransport / a -via tcp launch).
Events.On('battery:transport', (evt) => {
  const t = evt?.data || {};
  if (t.host) tcpHostInput.value = t.host;
  if (t.port) tcpPortInput.value = t.port;
  if (t.mode) setTransportUI(t.mode);
});

scanBtn.addEventListener('click', () => (transport === 'tcp' ? doConnectTCP() : doScan()));
modeBLEBtn.addEventListener('click', () => switchTransport('ble'));
modeTCPBtn.addEventListener('click', () => switchTransport('tcp'));
tcpHostInput.addEventListener('change', applyTcpSettings);
tcpPortInput.addEventListener('change', applyTcpSettings);
tcpHostInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') doConnectTCP(); });
tcpPortInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') doConnectTCP(); });
document.getElementById('back-btn').addEventListener('click', () => disconnect(false));
document.getElementById('refresh-btn').addEventListener('click', async () => {
  try {
    const r = await BatteryService.GetReading();
    renderReading(r);
  } catch (err) {
    console.error(err);
    showError(errorMessage(err));
  }
});

const gaugeCard = document.querySelector('.gauge-card');
gaugeCard?.addEventListener('dblclick', (e) => {
  e.preventDefault();
  e.stopPropagation();
  setSocFocus(!socFocus);
});

// Restore the transport the backend was launched with (-via / -host / -port).
// No automatic Bluetooth scan at start; WiFi/TCP is the default and waits for
// the user to press Connect (flaky hotspot links are easier to diagnose when
// the first dial is explicit).
async function initTransport() {
  try {
    const info = await BatteryService.GetTransport();
    if (info) {
      if (info.host) tcpHostInput.value = info.host;
      if (info.port) tcpPortInput.value = info.port;
      setTransportUI(info.mode);
    } else {
      setTransportUI('tcp');
    }
  } catch (err) {
    console.error('GetTransport failed:', err);
    setTransportUI('tcp');
  }
  if (transport === 'tcp') {
    scanStatus.textContent = 'Ready. Press Connect to start watching (fresh link per reading).';
  } else {
    scanStatus.textContent = 'Ready to scan for Bluetooth batteries.';
  }
}

showView('scan');
renderDevices([]);
clearAutoRetry();
initTransport();
