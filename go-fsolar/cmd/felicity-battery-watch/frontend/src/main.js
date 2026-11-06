import {Events, WML} from "@wailsio/runtime";
import {BatteryService} from "../bindings/felicity-battery-watch";

// Wire up data-wml-openURL links once the DOM is ready.
WML.Enable();

const POLL_INTERVAL_S = 60;

const scanView = document.getElementById('scan-view');
const detailView = document.getElementById('detail-view');
const scanBtn = document.getElementById('scan-btn');
const scanStatus = document.getElementById('scan-status');
const deviceList = document.getElementById('device-list');
const emptyState = document.getElementById('empty-state');
const errorBanner = document.getElementById('error-banner');
const statusDot = document.getElementById('status-dot');

const detailName = document.getElementById('detail-name');
const detailSn = document.getElementById('detail-sn');
const socValue = document.getElementById('soc-value');
const socBar = document.getElementById('soc-bar');
const kwhValue = document.getElementById('kwh-value');
const updatedLabel = document.getElementById('updated-label');
const voltageValue = document.getElementById('voltage-value');
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

function setStatusDot(state) {
  statusDot.className = `status-dot ${state || ''}`;
}

function showView(viewName) {
  const isDetail = viewName === 'detail';
  scanView.hidden = isDetail;
  detailView.hidden = !isDetail;
  if (!isDetail) stopCountdown();
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

function renderReading(r) {
  if (!r) return;
  socValue.textContent = fmt(r.soc, 0);
  const pct = Math.min(100, Math.max(0, Number(r.soc) || 0));
  socBar.style.width = `${pct}%`;
  kwhValue.textContent = fmt(r.remainingKWh, 2);
  voltageValue.textContent = fmt(r.voltage, 2);
  currentValue.textContent = fmt(r.current, 1);
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
  if (scanInFlight) return;
  scanInFlight = true;
  showError('');
  renderDevices([]);
  scanStatus.textContent = 'Scanning 10s for Felicity batteries…';
  scanBtn.classList.add('is-loading');
  scanBtn.disabled = true;
  setStatusDot('scanning');
  try {
    const devices = await BatteryService.Scan(10);
    renderDevices(devices || []);
    const f = (devices || []).filter(d => d.isFelicity).length;
    scanStatus.textContent = devices?.length
      ? `Found ${devices.length} device(s) (${f} Felicity). Tap one to connect.`
      : 'Scan complete — no devices found. Check the battery is powered and nearby.';
  } catch (err) {
    console.error(err);
    showError(errorMessage(err));
    scanStatus.textContent = 'Scan failed. See error above.';
  } finally {
    scanInFlight = false;
    scanBtn.classList.remove('is-loading');
    scanBtn.disabled = false;
    setStatusDot('');
  }
}

async function connect(id, name, devSn) {
  if (!id || connectingId) return;
  connectingId = id;
  showError('');
  scanStatus.textContent = `Connecting to ${name || id}…`;
  setStatusDot('scanning');
  // Clear before Connect: the backend emits the first reading
  // synchronously inside Connect(), so resetting after the await
  // would erase that already-rendered SOC/kWh.
  socValue.textContent = '--';
  kwhValue.textContent = '--';
  voltageValue.textContent = '--';
  currentValue.textContent = '--';
  capacityValue.textContent = '--';
  socBar.style.width = '0%';
  updatedLabel.textContent = 'Waiting for first reading…';
  try {
    await BatteryService.Connect(id);
    connectedId = id;
    detailName.textContent = name || id;
    detailSn.textContent = devSn ? `DevSN ${devSn}` : `ID ${id}`;
    showView('detail');
    setStatusDot('connected');
    showToast('Connected', `${name || id} · reading every ${POLL_INTERVAL_S}s`);
    startCountdown();
  } catch (err) {
    console.error(err);
    showError(`Connect failed: ${errorMessage(err)}`);
    scanStatus.textContent = 'Connection failed — try scanning again.';
    setStatusDot('');
  } finally {
    connectingId = null;
    renderDevicesSafe();
  }
}

let lastDevicesCache = [];
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
  setStatusDot('');
  scanStatus.textContent = 'Disconnected. Press Scan to find batteries again.';
  if (!silent) showToast('Disconnected', 'Link closed.');
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
    setStatusDot('connected');
    if (c.error) {
      showError(c.error);
      showToast('Connected', c.error);
    }
  } else {
    setStatusDot('');
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
  updatedLabel.textContent = 'Link closed — go Back and rescan.';
  connectedId = null;
}

scanBtn.addEventListener('click', doScan);
document.getElementById('back-btn').addEventListener('click', () => disconnect(false));
document.getElementById('disconnect-btn').addEventListener('click', () => disconnect(false));
document.getElementById('refresh-btn').addEventListener('click', async () => {
  try {
    const r = await BatteryService.GetReading();
    renderReading(r);
  } catch (err) {
    console.error(err);
    showError(errorMessage(err));
  }
});

// Initial state: nudge into a first scan.
showView('scan');
statusDot.className = 'status-dot';
renderDevices([]);
