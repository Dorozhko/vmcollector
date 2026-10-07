# VM Collector

**Lightweight Modbus TCP & Linux system metrics collector for VictoriaMetrics.**

VM Collector collects metrics from industrial devices and Linux systems and sends them directly to VictoriaMetrics using Remote Write.

<p align="center">
  <strong>Modbus TCP</strong>
  &nbsp;→&nbsp;
  <strong>VM Collector</strong>
  &nbsp;→&nbsp;
  <strong>VictoriaMetrics</strong>
</p>

---

## Features

- **Modbus TCP**
  - Multiple controllers
  - Manual register configuration
  - Per-register enable/disable
  - Configurable data types
  - Configurable byte and word order
  - Polling interval
  - Timeout and retry control

- **Linux System Metrics**
  - CPU usage
  - Memory usage
  - Storage usage
  - Temperature sensors
  - Network metrics
  - Uptime
  - File-based metrics

- **VictoriaMetrics**
  - Remote Write
  - Configurable endpoint
  - Configurable timeout
  - Runtime enable/disable
  - No connection attempts when disabled

- **Embedded Web UI**
  - Configuration management
  - Modbus controller management
  - Modbus register management
  - System metric management
  - VictoriaMetrics status
  - Per-metric status
  - Collector statistics
  - Diagnostic logs

- **Security**
  - Administrator authentication
  - bcrypt password hashing
  - Protected configuration file

- **Deployment**
  - Standalone Go binary
  - Snap package
  - RISC-V64 support

---
Remote access
-------------
http://<COLLECTOR_IP>:8888
-------------

CLI
____
./modbus-vmagent -set-admin-password
./modbus-vmagent -config config.json


## Architecture

```text
 ┌──────────────────┐
 │   Modbus TCP     │
 │   PLC / Devices  │
 └────────┬─────────┘
          │
          ▼
 ┌────────────────────────────┐
 │        VM Collector        │
 │                            │
 │  Modbus TCP  │  System     │
 │  Registers   │  Metrics    │
 │              │             │
 │       Embedded Web UI      │
 └──────────────┬─────────────┘
                │
          Remote Write
                │
                ▼
       ┌─────────────────┐
       │ VictoriaMetrics │
       └─────────────────┘
