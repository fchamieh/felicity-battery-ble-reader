# 1. Define network credentials
$SSID = "F072604825024250772"
$Password = "12345678"

# 2. Build the manual connection XML structure natively
$ProfileXml = @"
<?xml version="1.0"?>
<WLANProfile xmlns="http://www.microsoft.com/networking/WLAN/profile/v1">
    <name>$SSID</name>
    <SSIDConfig>
        <SSID>
            <name>$SSID</name>
        </SSID>
    </SSIDConfig>
    <connectionType>ESS</connectionType>
    <connectionMode>manual</connectionMode>
    <MSM>
        <security>
            <authEncryption>
                <authentication>WPA2PSK</authentication>
                <encryption>AES</encryption>
                <useX4Key>false</useX4Key>
            </authEncryption>
            <sharedKey>
                <keyType>passPhrase</keyType>
                <protected>false</protected>
                <keyPassPhrase>$Password</keyPassPhrase>
            </sharedKey>
        </security>
    </MSM>
</WLANProfile>
"@

# 3. Output the XML to a temporary file
$TempPath = "$env:TEMP\wifi-profile.xml"
$ProfileXml | Out-File -FilePath $TempPath -Encoding ascii

# 4. Import the profile and connect using native netsh
netsh wlan add profile filename=$TempPath
netsh wlan connect ssid=$SSID name=$SSID

# 5. Clean up the temporary file
Remove-Item -Path $TempPath
