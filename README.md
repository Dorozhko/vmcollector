# modbus-vmagent

Lightweight Go Modbus TCP collector designed around the VictoriaMetrics vmagent remote-write model.

## Current V1

- JSON configuration using Go standard library `encoding/json`.
- Modbus TCP input.
- FC03 holding-register reads.
- INT16, UINT16, INT32, UINT32 and FLOAT32 decoding.
- Direct VictoriaMetrics Remote Write v1 output using protobuf + zstd.
- Minimal HTTP status page.
- RISC-V/ARM64-friendly single Go binary design.

## Configuration

```bash
./modbus-vmagent -config=/path/to/config.json
```

See `config.example.json`.

## VictoriaMetrics

The default output is direct VictoriaMetrics Remote Write:

```text
POST /api/v1/write
Content-Type: application/x-protobuf
Content-Encoding: zstd
X-VictoriaMetrics-Remote-Write-Version: 1
```

The wire format follows the current VictoriaMetrics remote-write implementation used by vmagent.

## Development

```bash
go build ./cmd/modbus-vmagent
```
