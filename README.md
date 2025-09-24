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

# Specify transport (defaults to udp). Supported: udp, tcp, tls, ws, wss
cipbx --transport udp -l 127.0.0.1 -p 5090

# With authentication (accepts REGISTER requests)
cipbx -l 127.0.0.1 -p 5090 -u username -w password

# With timeout (automatically hang up after 30 seconds)
cipbx -l 127.0.0.1 -p 5090 -t 30

# With RTP payload validation (expect 0x55 in all payload bytes)
cipbx -l 127.0.0.1 -p 5090 --expect 0x55
```

### Features

- **Echo Server**: Call `echo@<server-ip>` to test echo functionality
- **Playback Server**: Call `playback@<server-ip>` for demo playback functionality
- **Call Bridging**: Call any other extension to bridge calls (e.g., `alice@<server-ip>`)
- **Authentication**: Optional digest authentication for REGISTER requests with 1-hour expiration
- **Call Timeout**: Optional automatic call termination after specified duration (in seconds)
- **Transport Selection**: Choose between `udp`, `tcp`, `tls`, `ws`, `wss` (default `udp`)
- **RTP Payload Validation**: Optional validation of RTP payload bytes for testing purposes

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

### Call Timeout

The timeout feature allows you to specify a maximum call duration in seconds. When enabled:
- Calls will be automatically terminated after the specified duration
- The server sends a BYE message to properly close the call
- Useful for testing scenarios where calls should not run indefinitely

Example with timeout:
```bash
# Set 60-second timeout
cipbx -l 127.0.0.1 -p 5090 -t 60

# Combine with authentication
cipbx -l 127.0.0.1 -p 5090 -u testuser -w testpass -t 120
```

### RTP Payload Validation

The `--expect` flag enables RTP payload validation for testing purposes. When specified, the server will:

- Ignore the first 15 RTP packets (startup/comfort noise)
- Validate a window of payloads (≥50 packets over 3 seconds)
- Check that every payload byte matches the expected value
- Log validation results with `RTP_ASSERT_OK` or `RTP_ASSERT_FAIL`

Example usage:
```bash
# Expect 0x55 in all RTP payload bytes
cipbx -l 127.0.0.1 -p 5090 --expect 0x55

# Combine with other options
cipbx -l 127.0.0.1 -p 5090 -u testuser -w testpass --expect 0x55 -t 60
```

The validation is only active for `echo@domain` calls and logs results like:
- `RTP_ASSERT_OK codec=PCMU bytes=0x55` (success)
- `RTP_ASSERT_FAIL codec=PCMU expected=0x55 valid_packets=45 total_packets=50` (failure)


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