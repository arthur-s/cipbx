# cipbx

"CI PBX" is a VoIP echo server, designed for CI/CD pipelines. Implemented in Go using [diago](https://github.com/emiago/diago).

## Purpose

- Test SIP/RTP call flows in automated environments
- Validate call establishment and media transmission
- Check codec negotiation
- Provide reliable testing for continuous integration pipelines


## Usage

```bash
# Basic usage (echo server only)
cipbx --listen 127.0.0.1 --port 5090

# Using short flags
cipbx -l 127.0.0.1 -p 5090

# With authentication (accepts REGISTER requests)
cipbx -l 127.0.0.1 -p 5090 -u username -w password
```

### Features

- **Echo Server**: Call `echo@<server-ip>` to test echo functionality
- **Playback Server**: Call `playback@<server-ip>` for demo playback functionality
- **Call Bridging**: Call any other extension to bridge calls (e.g., `alice@<server-ip>`)
- **Authentication**: Optional digest authentication for REGISTER requests with 1-hour expiration

### Call Routing

- **echo@domain**: Routes to echo server for testing media transmission
- **playback@domain**: Routes to playback server (demo functionality)
- **user@domain**: Bridges calls to the specified user (requires proper SIP routing)

### Authentication

When username and password are provided:
- Accepts authenticated REGISTER requests
- Sets registration expiration to 1 hour
- Logs successful registrations

Example with authentication:
```bash
# Enable authentication
cipbx -l 127.0.0.1 -p 5090 -u testuser -w testpass

# Client can now register with:
# REGISTER sip:127.0.0.1:5090 SIP/2.0
# Authorization: Digest username="testuser", realm="cipbx", ...
```


# RTP tester

RTP testing utility for validating media transmission without SIP signaling.

## 🎯 Purpose

- Test raw RTP media streams directly
- Validate codec performance (PCMA, PCMU, Opus)
- Debug RTP packet transmission issues
- Measure media quality without SIP overhead

## 🔄 Difference from cipbx

While `cipbx` tests complete SIP/RTP call flows, `rtptester` focuses exclusively on the media layer:

- **rtptester**: Raw RTP testing only (no SIP)
- **cipbx**: Complete SIP + RTP call testing

## Basic Usage

```bash
# With custom values using flags
rtptester --local-ip 192.168.1.100 --local-port 6000 --remote-ip 192.168.1.200 --remote-port 6001 --codec PCMU --debug

# Using short flags
rtptester -l 192.168.1.100 -p 6000 -r 192.168.1.200 -P 6001 -c PCMU -d
```

## Manual Testing with GStreamer

The RTP tester can be manually tested using GStreamer to send and receive RTP packets. This is useful for verifying the echo functionality works correctly.

### Test Setup

Run these commands in separate terminals:

**Terminal 1 - RTP Echo Tester:**
```bash
.\rtptester --local-ip 127.0.0.1 --local-port 5004 --remote-ip 127.0.0.1 --remote-port 5005 --codec PCMA --debug
```

**Terminal 2 - GStreamer Sender:**
```bash
gst-launch-1.0 audiotestsrc wave=sine freq=440 num-buffers=300 ! audioconvert ! audioresample ! alawenc ! rtppcmapay ! udpsink host=127.0.0.1 port=5004
```

**Terminal 3 - GStreamer Receiver:**
```bash
gst-launch-1.0 udpsrc port=5005 ! application/x-rtp,encoding-name=PCMA,clock-rate=8000 ! rtppcmadepay ! alawdec ! audioconvert ! wavenc ! filesink location=received_audio.wav
```

### Expected Results

- The RTP tester will log received and echoed packets
- The `received_audio.wav` file will contain the echoed audio
- You should hear a 440Hz sine wave when playing the audio file

### Direct GStreamer Test (Bypass RTP Tester)

To verify GStreamer setup works independently:

**Terminal 1 - Direct Sender:**
```bash
gst-launch-1.0 audiotestsrc wave=sine freq=440 num-buffers=300 ! audioconvert ! audioresample ! alawenc ! rtppcmapay ! udpsink host=127.0.0.1 port=5005
```

**Terminal 2 - Direct Receiver:**
```bash
gst-launch-1.0 udpsrc port=5005 ! application/x-rtp,encoding-name=PCMA,clock-rate=8000 ! rtppcmadepay ! alawdec ! audioconvert ! wavenc ! filesink location=direct_test.wav
```

This should create `direct_test.wav` with audio content, confirming GStreamer is working correctly.