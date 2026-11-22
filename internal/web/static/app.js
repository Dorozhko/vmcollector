let cfg = {};
let editing = null;
let logs = [];
let logFilter = "all";

const state = {
    metricStatuses: {}
};

const $ = id => document.getElementById(id);

const sourceNames = {
    cpu: "CPU",
    memory: "RAM Usage",
    storage: "Storage",
    directory: "Directory",
    temperature: "Temperature",
    network: "Network",
    uptime: "Uptime",
    file: "File",
    modbus: "Modbus TCP"
};

async function load() {
    try {
        const r = await fetch("/api/config");
        if (!r.ok) throw new Error(await r.text());

        cfg = await r.json();
        if (!cfg.output) cfg.output = {};
        if (!cfg.output.victoriametrics) cfg.output.victoriametrics = {};
        if (!cfg.output.victoriametrics.address) cfg.output.victoriametrics.address = "http://127.0.0.1:8428";
        if (typeof cfg.output.victoriametrics.enabled !== "boolean") cfg.output.victoriametrics.enabled = true;
        if (!cfg.output.websocket) cfg.output.websocket = {};
        if (typeof cfg.output.websocket.enabled !== "boolean") cfg.output.websocket.enabled = false;
        if (!Number.isInteger(Number(cfg.output.websocket.port)) || Number(cfg.output.websocket.port) < 1 || Number(cfg.output.websocket.port) > 65535) cfg.output.websocket.port = 8889;
        else cfg.output.websocket.port = Number(cfg.output.websocket.port);

        if (!cfg.system) {
            cfg.system = {
                enabled: true,
                poll_interval: "5s",
                metrics: []
            };
        }

        if (!cfg.system.metrics) {
            cfg.system.metrics = [];
        }

        render();
        startWebSocket();
    } catch (e) {
        showError(e.message);
    }
}

function allMetrics() {
    const out = [];

    (cfg.system?.metrics || []).forEach((m, i) => {
        out.push({
            ...m,
            key: `system:${i}`,
            _kind: "system",
            _i: i
        });
    });

    (cfg.modbus?.registers || []).forEach((m, i) => {
        out.push({
            ...m,
            key: `modbus:${m.name}:${i}`,
            _kind: "modbus",
            _i: i
        });
    });

    return out;
}

function render() {
    renderMetrics();
    renderJSON();

    const vm = cfg.output?.victoriametrics || {};
    if ($("vm-address")) $("vm-address").value = vm.address || "";
    if ($("vm-toggle")) $("vm-toggle").checked = vm.enabled !== false;

    const ws = cfg.output?.websocket || {};
    if ($("ws-port")) $("ws-port").value = ws.port || 8889;
    if ($("ws-toggle")) $("ws-toggle").checked = ws.enabled === true;
    if ($("ws-token")) $("ws-token").value = ws.token || "";
    renderWSStatus(ws.enabled === true);
}

function statusText(key, enabled) {
    if (!enabled) {
        return "OFF";
    }

    const status = state.metricStatuses?.[key];

    if (status === "OK" || status === "ERROR") {
        return status;
    }

    return "—";
}

function statusClass(key, enabled) {
    if (!enabled) {
        return "disabled";
    }

    const status = state.metricStatuses?.[key];

    switch (status) {
        case "OK":
            return "ok";
        case "ERROR":
            return "error";
        default:
            return "pending";
    }
}

function renderMetrics() {
    const rows = $("rows");
    const metrics = allMetrics();

    if (!metrics.length) {
        rows.innerHTML = `
            <tr>
                <td colspan="9" class="empty">
                    No metrics configured
                </td>
            </tr>
        `;
        return;
    }

    rows.innerHTML = metrics.map((m, i) => {

        const enabled = m.enabled !== false;
        const vmEnabled = m.victoriametrics_enabled !== false;
        const wsEnabled = m.websocket_enabled === true;

        const source =
            m._kind === "modbus"
                ? "Modbus TCP"
                : sourceNames[m.source] || m.source || "—";

        const sourceCategory =
            m._kind === "modbus" || m.source === "modbus"
                ? "modbus"
                : ["storage", "directory", "file"].includes(m.source)
                    ? "storage"
                    : "system";

        const metric = displayMetricName(m);

        const details = metricDetails(m);

        return `
            <tr class="source-row-${sourceCategory}">
                <td>
                    <label class="metric-switch metric-switch--master" title="Enable metric">
                        <input
                            class="metric-switch-input"
                            type="checkbox"
                            ${enabled ? "checked" : ""}
                            aria-label="Enable ${esc(m.name)}"
                            onchange="toggleMetric(${i}, this.checked)"
                        >
                        <span class="metric-switch-slider"></span>
                    </label>
                </td>

                <td>
                    <span class="metric-status ${statusClass(m.key, enabled)}">
                        <span></span>
                        ${statusText(m.key, enabled)}
                    </span>
                </td>

                <td>
                    <label class="metric-switch" title="VictoriaMetrics output">
                        <input
                            class="metric-switch-input"
                            type="checkbox"
                            aria-label="VictoriaMetrics output for ${esc(m.name)}"
                            ${vmEnabled ? "checked" : ""}
                            onchange="toggleMetricOutput(${i}, 'victoriametrics_enabled', this.checked)"
                        >
                        <span class="metric-switch-slider"></span>
                    </label>
                </td>

                <td>
                    <label class="metric-switch" title="WebSocket output">
                        <input
                            class="metric-switch-input"
                            type="checkbox"
                            aria-label="WebSocket output for ${esc(m.name)}"
                            ${wsEnabled ? "checked" : ""}
                            onchange="toggleMetricOutput(${i}, 'websocket_enabled', this.checked)"
                        >
                        <span class="metric-switch-slider"></span>
                    </label>
                </td>

                <td class="metric-name">
                    ${esc(m.name)}
                </td>

                <td>
                    <span class="source-badge source-${sourceCategory}">
                        ${esc(source)}
                    </span>
                </td>

                <td>
                    ${esc(metric)}
                </td>

                <td class="details">
                    ${esc(details)}
                </td>

                <td class="actions">
                    <button onclick="editMetric(${i})">
                        Edit
                    </button>

                    <button
                        class="danger"
                        onclick="deleteMetric(${i})"
                    >
                        Delete
                    </button>
                </td>
            </tr>
        `;
    }).join("");
}

function displayMetricName(m) {
    if (m.source === "cpu") {
        const names = {
            usage: "CPU Usage (%)",
            core_usage: "CPU Core Usage (%)",
            core_frequency: "CPU Core Frequency (MHz)"
        };
        if (names[m.metric]) return names[m.metric];
    }

    if (m.source === "memory") {
        if (m.metric === "used_mb") return "RAM Usage (MB)";
        if (m.metric === "used_percent") return "RAM Usage (%)";
    }

    return m.metric || m.type || "—";
}

function memoryMetricName(metric) {
    return metric === "used_mb"
        ? "ram_used_mb"
        : "ram_used_percent";
}

function cpuMetricName(metric) {
    const names = {
        usage: "cpu_usage_percent",
        core_usage: "cpu_core_usage_percent",
        core_frequency: "cpu_core_frequency_mhz"
    };

    return names[metric] || "cpu_usage_percent";
}

function metricDetails(m) {
    const device = m.device || "";
    const unitID = m.unit_id ?? "—";
    const address = m.address ?? "—";
    const fn = m.function || "holding";

    const location = device
        ? `${device} · Unit ${unitID}`
        : `Unit ${unitID}`;

    return `${location} · ${fn} · ${address}`;
}

function renderJSON() {
    $("json").value = JSON.stringify(cfg, null, 2);
}

function toggleMetric(index, value) {
    const m = allMetrics()[index];

    if (!m) return;

    if (m._kind === "system") {
        cfg.system.metrics[m._i].enabled = value;
    } else {
        cfg.modbus.registers[m._i].enabled = value;
    }

    render();
}

function toggleMetricOutput(index, output, value) {
    if (output !== "victoriametrics_enabled" && output !== "websocket_enabled") {
        return;
    }

    const m = allMetrics()[index];
    if (!m) return;

    const target = m._kind === "system"
        ? cfg.system.metrics[m._i]
        : cfg.modbus.registers[m._i];

    if (!target) return;

    target[output] = value;
    renderJSON();
}

function deleteMetric(index) {
    const m = allMetrics()[index];

    if (!m) return;

    if (!confirm(`Delete "${m.name}"?`)) {
        return;
    }

    if (m._kind === "system") {
        cfg.system.metrics.splice(m._i, 1);
    } else {
        cfg.modbus.registers.splice(m._i, 1);
    }

    render();
}

function editMetric(index) {
    const m = allMetrics()[index];

    if (!m) return;

    editing = {
        kind: m._kind,
        index: m._i
    };

    openEditor(m);
}

function addMetric() {
    editing = null;

    openEditor({
        enabled: true,
        name: "new_metric",
        source: "cpu",
        metric: "usage"
    });
}

function openEditor(m) {
    $("modal-title").textContent =
        editing ? "Edit Metric" : "Add Metric";

    $("f-name").value = m.name || "";
    $("f-source").value =
        m._kind === "modbus"
            ? "modbus"
            : m.source || "cpu";

    setPollInterval(m.poll_interval || "5s");
    renderDynamicFields(m);
    updateMemoryNameField();

    const typeField = $("f-type");

    if (typeField) {
        typeField.addEventListener("change", () => {
            const orderFields = $("float32-order-fields");

            if (orderFields) {
                orderFields.classList.toggle(
                    "hidden",
                    typeField.value !== "float32"
                );
            }
        });
    }

    $("modal").classList.remove("hidden");
}

function closeEditor() {
    $("modal").classList.add("hidden");
    editing = null;
}

function renderDynamicFields(m) {

    const source = $("f-source").value;
    const container = $("dynamic-fields");

    if (source === "cpu") {

        container.innerHTML = `
            ${selectField(
                "Metric",
                "f-metric",
                [
                    "usage",
                    "core_usage",
                    "core_frequency"
                ],
                m.metric || "usage"
            )}
        `;

        return;
    }

    if (source === "memory") {

        container.innerHTML = `
            ${selectField(
                "Metric",
                "f-metric",
                ["used_mb", "used_percent"],
                m.metric || "used_percent"
            )}
        `;

        return;
    }

    if (source === "storage") {
        container.innerHTML = `
            ${inputField("Mount", "f-path", m.path || "/")}
            ${selectField(
                "Metric",
                "f-metric",
                [
                    "used_mb",
                    "used_gb",
                    "available_mb",
                    "available_gb",
                    "total_mb",
                    "total_gb",
                    "used_percent",
                    "available_percent"
                ],
                m.metric || "used_percent"
            )}
        `;
        return;
    }

    if (source === "directory") {
        container.innerHTML = `
            ${inputField("Directory Path", "f-path", m.path || "")}
            ${selectField(
                "Metric",
                "f-metric",
                ["size_mb", "size_gb"],
                m.metric || "size_gb"
            )}
        `;
        return;
    }

    if (source === "temperature") {

        container.innerHTML = `
            ${inputField(
                "Path",
                "f-path",
                m.path || "/sys/class/thermal/thermal_zone0/temp"
            )}

            ${inputField(
                "Metric",
                "f-metric",
                m.metric || "value"
            )}

            ${numberField(
                "Scale",
                "f-scale",
                m.scale ?? 1
            )}

            ${numberField(
                "Offset",
                "f-offset",
                m.offset ?? 0
            )}
        `;

        return;
    }

    if (source === "network") {

        container.innerHTML = `
            ${inputField(
                "Interface",
                "f-path",
                m.path || "eth0"
            )}

            ${selectField(
                "Metric",
                "f-metric",
                [
                    "rx_bytes",
                    "tx_bytes",
                    "rx_packets",
                    "tx_packets",
                    "rx_errors",
                    "tx_errors"
                ],
                m.metric || "rx_bytes"
            )}
        `;

        return;
    }

    if (source === "uptime") {

        container.innerHTML = `
            <div class="field">
                <label>Metric</label>
                <input
                    id="f-metric"
                    value="seconds"
                    readonly
                >
            </div>
        `;

        return;
    }

    if (source === "file") {
        const isLegacyFile =
            Object.keys(m || {}).length > 0 &&
            !["size_mb", "size_gb"].includes(m.metric);

        if (isLegacyFile) {
            // Preserve the legacy file metric with its scale and offset.
            container.innerHTML = `
                ${inputField("File Path", "f-path", m.path || "")}
                ${numberField("Scale", "f-scale", m.scale ?? 1)}
                ${numberField("Offset", "f-offset", m.offset ?? 0)}
            `;
        } else {
            container.innerHTML = `
                ${inputField("File Path", "f-path", m.path || "")}
                ${selectField(
                    "Metric",
                    "f-metric",
                    ["size_mb", "size_gb"],
                    m.metric || "size_mb"
                )}
            `;
        }
        return;
    }

    if (source === "modbus") {

        container.innerHTML = renderModbusFields(m);

        return;
    }
}

function renderModbusFields(m) {
    const endpoint = m.device || "";
    const unitID = m.unit_id || 1;

    return `
        ${inputField(
            "IP Address:Port",
            "f-device-endpoint",
            endpoint
        )}

        ${numberField(
            "Unit ID",
            "f-unit-id",
            unitID || 1
        )}

        ${inputField(
            "Address",
            "f-address",
            m.address ?? 40001
        )}

        ${selectField(
            "Function",
            "f-function",
            [
                "holding",
                "input"
            ],
            m.function || "holding"
        )}

        ${selectField(
            "Data Type",
            "f-type",
            [
                "uint16",
                "int16",
                "uint32",
                "int32",
                "float32"
            ],
            m.type || "uint16",
            {
                "uint16": "Unsigned 16-bit",
                "int16": "Signed 16-bit",
                "uint32": "Unsigned 32-bit",
                "int32": "Signed 32-bit",
                "float32": "Floating Point 32-bit"
            }
        )}

        <div id="float32-order-fields" class="${m.type === "float32" ? "" : "hidden"}">
            ${selectField(
                "Byte Order",
                "f-byte-order",
                [
                    "ABCD",
                    "BADC",
                    "CDAB",
                    "DCBA"
                ],
                m.byte_order && ["ABCD", "BADC", "CDAB", "DCBA"].includes(m.byte_order)
                    ? m.byte_order
                    : "ABCD"
            )}

            ${selectField(
                "Word Order",
                "f-word-order",
                [
                    "AB",
                    "BA"
                ],
                m.word_order && ["AB", "BA"].includes(m.word_order)
                    ? m.word_order
                    : "AB"
            )}
        </div>

        ${numberField(
            "Offset",
            "f-offset",
            m.offset ?? 0
        )}
    `;
}


function saveMetric() {

    const name = $("f-name").value.trim();
    const source = $("f-source").value;

    if (!name) {
        alert("Metric name is required");
        return;
    }

    const pollInterval = readPollInterval();

    if (!pollInterval) {
        return;
    }

    if (source === "modbus") {

        if (!saveModbusMetric(name, pollInterval)) {
            return;
        }

    } else {

        saveSystemMetric(name, source, pollInterval);
    }

    closeEditor();
    render();
}

function saveSystemMetric(name, source, pollInterval) {

    const selectedMetric = $("f-metric")?.value;

    if (source === "memory") {
        name = memoryMetricName(selectedMetric);
        $("f-name").value = name;
    } else if (source === "cpu") {
        name = cpuMetricName(selectedMetric);
        $("f-name").value = name;
    } else if (
        ["storage", "directory"].includes(source) ||
        (source === "file" && ["size_mb", "size_gb"].includes(selectedMetric))
    ) {
        name = `${source}_${selectedMetric}`;
        $("f-name").value = name;
    }

    const metric = {
        enabled: true,
        victoriametrics_enabled: true,
        websocket_enabled: false,
        name,
        source,
        poll_interval: pollInterval
    };

    const metricElement = $("f-metric");

    if (metricElement) {
        metric.metric = metricElement.value;
    }

    const pathElement = $("f-path");

    if (pathElement && pathElement.value) {
        metric.path = pathElement.value;
    }

    const scaleElement = $("f-scale");

    if (scaleElement) {
        metric.scale = Number(scaleElement.value);
    }

    const offsetElement = $("f-offset");

    if (offsetElement) {
        metric.offset = Number(offsetElement.value);
    }

    if (editing && editing.kind === "system") {

        const old =
            cfg.system.metrics[editing.index];

        metric.enabled =
            old.enabled !== false;
        metric.victoriametrics_enabled =
            old.victoriametrics_enabled !== false;
        metric.websocket_enabled =
            old.websocket_enabled === true;

        cfg.system.metrics[editing.index] = metric;

    } else {

        cfg.system.metrics.push(metric);
    }
}

function saveModbusMetric(name, pollInterval) {

    if (!cfg.modbus) {
        cfg.modbus = {};
    }

    if (!Array.isArray(cfg.modbus.registers)) {
        cfg.modbus.registers = [];
    }

    const endpoint =
        $("f-device-endpoint").value.trim();

    const unitID =
        Number($("f-unit-id").value);

    const address =
        Number($("f-address").value);

    if (!endpoint) {
        alert("IP Address:Port is required");
        return false;
    }

    // IPv4 / hostname + port.
    const separator =
        endpoint.lastIndexOf(":");

    if (separator <= 0 ||
        separator === endpoint.length - 1) {
        alert("Enter IP Address and Port, for example 192.168.1.10:502");
        return false;
    }

    const deviceAddress =
        endpoint.slice(0, separator).trim();

    const devicePort =
        Number(endpoint.slice(separator + 1));

    if (!deviceAddress) {
        alert("IP Address is required");
        return false;
    }

    if (!Number.isInteger(devicePort) ||
        devicePort < 1 ||
        devicePort > 65535) {
        alert("Port must be between 1 and 65535");
        return false;
    }

    if (!Number.isInteger(unitID) ||
        unitID < 1 ||
        unitID > 247) {
        alert("Unit ID must be between 1 and 247");
        return false;
    }

    if (!Number.isInteger(address) ||
        address < 1 ||
        address > 65535) {
        alert("Address must be between 1 and 65535");
        return false;
    }

    const metric = {
        enabled: true,
        victoriametrics_enabled: true,
        websocket_enabled: false,
        name,
        device: `${deviceAddress}:${devicePort}`,
        unit_id: unitID,
        address,
        function: $("f-function").value,
        type: $("f-type").value,
        poll_interval: pollInterval,
        offset: Number($("f-offset").value) || 0
    };

    if (metric.type === "float32") {
        metric.byte_order =
            $("f-byte-order").value || "ABCD";

        metric.word_order =
            $("f-word-order").value || "AB";
    }

    if (editing && editing.kind === "modbus") {

        const old =
            cfg.modbus.registers[editing.index];

        metric.enabled =
            old?.enabled !== false;
        metric.victoriametrics_enabled =
            old?.victoriametrics_enabled !== false;
        metric.websocket_enabled =
            old?.websocket_enabled === true;

        // Preserve existing labels.
        if (old?.labels) {
            metric.labels = old.labels;
        }

        cfg.modbus.registers[editing.index] =
            metric;

    } else {

        cfg.modbus.registers.push(metric);
    }

    return true;
}

function parseDurationMilliseconds(value) {
    if (typeof value !== "string") {
        return null;
    }

    const match = value.trim().match(/^(\d+(?:\.\d+)?)(ms|s|m)$/);

    if (!match) {
        return null;
    }

    const multiplier = {
        ms: 1,
        s: 1000,
        m: 60000
    }[match[2]];

    return Number(match[1]) * multiplier;
}

function setPollInterval(value) {
    const input = $("f-poll-interval");
    const unit = $("f-poll-unit");

    if (!input || !unit) {
        return;
    }

    const milliseconds = parseDurationMilliseconds(value) ?? 5000;

    if (milliseconds < 60000 && milliseconds % 1 === 0) {
        input.value = milliseconds;
        unit.value = "ms";
    } else if (milliseconds % 60000 === 0) {
        input.value = milliseconds / 60000;
        unit.value = "m";
    } else if (milliseconds % 1000 === 0) {
        input.value = milliseconds / 1000;
        unit.value = "s";
    } else {
        input.value = milliseconds;
        unit.value = "ms";
    }
}

function readPollInterval() {
    const input = $("f-poll-interval");
    const unit = $("f-poll-unit");
    const value = Number(input.value);

    if (!Number.isFinite(value) || value <= 0 || !Number.isInteger(value)) {
        alert("Poll Interval must be a positive whole number.");
        return null;
    }

    const multiplier = {
        ms: 1,
        s: 1000,
        m: 60000
    }[unit.value];

    const milliseconds = value * multiplier;

    if (!Number.isSafeInteger(milliseconds) ||
        milliseconds < 1 ||
        milliseconds > 3600000) {
        alert("Poll Interval must be between 1 millisecond and 60 minutes.");
        return null;
    }

    return `${value}${unit.value}`;
}

function inputField(label, id, value) {

    return `
        <div class="field">
            <label>${esc(label)}</label>
            <input
                id="${id}"
                type="text"
                value="${esc(value)}"
            >
        </div>
    `;
}

function numberField(label, id, value) {

    return `
        <div class="field">
            <label>${esc(label)}</label>
            <input
                id="${id}"
                type="number"
                step="any"
                value="${esc(value)}"
            >
        </div>
    `;
}

function selectField(label, id, options, selected, displayNames = {}) {

    return `
        <div class="field">
            <label>${esc(label)}</label>
            <select id="${id}">
                ${options.map(v => `
                    <option value="${esc(v)}"
                        ${v === selected ? "selected" : ""}>
                        ${esc(displayNames[v] || v)}
                    </option>
                `).join("")}
            </select>
        </div>
    `;
}

function selectFieldRaw(label, id, options) {

    return `
        <div class="field">
            <label>${esc(label)}</label>
            <select id="${id}">
                ${options}
            </select>
        </div>
    `;
}

async function save() {

    try {

        const parsed =
            JSON.parse($("json").value);

        if (!parsed.output) parsed.output = {};
        if (!parsed.output.victoriametrics) {
            parsed.output.victoriametrics = {};
        }

        if ($("vm-address")) {
            parsed.output.victoriametrics.address =
                $("vm-address").value.trim();
        }

        if ($("vm-toggle")) {
            parsed.output.victoriametrics.enabled =
                $("vm-toggle").checked;
        }

        if (!parsed.output.websocket) parsed.output.websocket = {};

        if ($("ws-port")) {
            const port = Number($("ws-port").value);
            if (!Number.isInteger(port) || port < 1 || port > 65535) {
                alert("WebSocket port must be between 1 and 65535");
                return;
            }
            parsed.output.websocket.port = port;
        }

        if ($("ws-toggle")) {
            parsed.output.websocket.enabled = $("ws-toggle").checked;
        }
        if ($("ws-token")) {
            parsed.output.websocket.token = $("ws-token").value.trim();
        }

        delete parsed.output.victoriametrics.url;
        delete parsed.output.victoriametrics.protocol;

        const response =
            await fetch("/api/config", {
                method: "PUT",
                headers: {
                    "Content-Type": "application/json"
                },
                body: JSON.stringify(parsed)
            });

        if (!response.ok) {
            throw new Error(
                await response.text()
            );
        }

        cfg = await response.json();

        render();
        startWebSocket();

        if (cfg.output?.victoriametrics?.enabled === false) {
            renderVMStatus("Disabled");
        } else {
            renderVMStatus("Disconnected");
            void checkVMHealth();
        }

    } catch (e) {

        alert(e.message);
    }
}

async function sendHeartbeat() {

    try {

        await fetch("/api/session/heartbeat", {
            method: "POST",
            cache: "no-store"
        });

    } catch {
        // Ignore temporary connection errors.
    }
}

async function loadMetricStatuses() {

    try {

        const response =
            await fetch("/api/metric-status", {
                cache: "no-store"
            });

        if (!response.ok) {
            throw new Error("metric status request failed");
        }

        state.metricStatuses =
            await response.json();

        renderMetrics();

    } catch {
        // Ignore temporary connection errors.
    }
}

let wsConnection = null;
let wsReconnectTimer = null;
let wsReconnectDelay = 1000;
let wsGeneration = 0;
let wsLastMessageAt = 0;
let wsSampleCount = 0;

function renderWSStatus(enabled) {
    const text = $("ws-status-text");
    if (!text) return;
    text.textContent = enabled === true ? "On" : "Off";
}

function stopWebSocket() {
    wsGeneration++;

    if (wsReconnectTimer !== null) {
        clearTimeout(wsReconnectTimer);
        wsReconnectTimer = null;
    }

    if (wsConnection !== null) {
        const socket = wsConnection;
        wsConnection = null;
        socket.onopen = null;
        socket.onmessage = null;
        socket.onerror = null;
        socket.onclose = null;
        if (socket.readyState < WebSocket.CLOSING) socket.close();
    }
}

function startWebSocket() {
    stopWebSocket();

    const wsConfig = cfg.output?.websocket || {};
    if (wsConfig.enabled !== true) {
        renderWSStatus(false);
        return;
    }

    const port = Number(wsConfig.port) || 8889;
    const generation = wsGeneration;
    const scheme = location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${scheme}//${location.hostname}:${port}/ws`;

    renderWSStatus(true);

    try {
        const socket = new WebSocket(url);
        wsConnection = socket;

        socket.onopen = () => {
            if (generation !== wsGeneration) return;
            wsReconnectDelay = 1000;
            renderWSStatus(true);
        };

        socket.onmessage = event => {
            if (generation !== wsGeneration) return;
            try {
                const message = JSON.parse(event.data);
                if (message.type !== "metrics" || !Array.isArray(message.samples)) return;

                wsLastMessageAt = Date.now();
                wsSampleCount = message.samples.length;

                // Keep the latest WebSocket payload available for the UI/debug console.
                state.websocketMetrics = message.samples;
                window.dispatchEvent(new CustomEvent("vmcollector:websocket-metrics", {
                    detail: message
                }));
            } catch (error) {
                console.error("Invalid WebSocket metrics message", error);
            }
        };

        socket.onerror = () => {
            if (generation === wsGeneration) renderWSStatus(true);
        };

        socket.onclose = () => {
            if (generation !== wsGeneration) return;
            wsConnection = null;
            renderWSStatus(true);
            wsReconnectTimer = setTimeout(() => {
                wsReconnectTimer = null;
                startWebSocket();
            }, wsReconnectDelay);
            wsReconnectDelay = Math.min(wsReconnectDelay * 2, 15000);
        };
    } catch (error) {
        renderWSStatus(true);
        console.error("WebSocket connection failed", error);
        wsReconnectTimer = setTimeout(startWebSocket, wsReconnectDelay);
        wsReconnectDelay = Math.min(wsReconnectDelay * 2, 15000);
    }
}

function renderVMStatus(status) {
    const element = $("vm-status");
    const text = $("vm-status-text");
    if (!element || !text) return;

    const normalized = ["Connected", "Disconnected", "Disabled"].includes(status)
        ? status
        : "Disconnected";

    element.className = "vm-status " + normalized.toLowerCase();
    text.textContent = normalized;
}

let vmHealthInFlight = false;
let vmHealthTimer = null;
let vmHealthController = null;

async function checkVMHealth() {
    if (document.hidden || vmHealthInFlight) return;

    vmHealthInFlight = true;
    vmHealthController = new AbortController();
    const timeout = setTimeout(() => vmHealthController.abort(), 2000);

    try {
        const response = await fetch("/api/vm-health", {
            cache: "no-store",
            signal: vmHealthController.signal
        });

        if (!response.ok) {
            renderVMStatus("Disconnected");
            return;
        }

        const data = await response.json();
        renderVMStatus(data.status);
    } catch {
        renderVMStatus("Disconnected");
    } finally {
        clearTimeout(timeout);
        vmHealthController = null;
        vmHealthInFlight = false;
    }
}

function startVMHealthChecks() {
    if (vmHealthTimer !== null || document.hidden) return;

    const tick = async () => {
        vmHealthTimer = null;
        if (document.hidden) return;

        await checkVMHealth();

        if (!document.hidden) {
            vmHealthTimer = setTimeout(tick, 1000);
        }
    };

    void tick();
}

function stopVMHealthChecks() {
    if (vmHealthTimer !== null) {
        clearTimeout(vmHealthTimer);
        vmHealthTimer = null;
    }

    if (vmHealthController) {
        vmHealthController.abort();
    }
}

document.addEventListener("visibilitychange", () => {
    if (document.hidden) {
        stopVMHealthChecks();
    } else {
        startVMHealthChecks();
    }
});

window.addEventListener("pagehide", stopVMHealthChecks);
window.addEventListener("pageshow", startVMHealthChecks);

async function updateStatus() {

    try {

        const response =
            await fetch("/api/status");

        const data =
            await response.json();

        const running =
            data.status === "running";

        $("status").textContent =
            running ? "Running" : "Stopped";

        $("status-dot").className =
            "status-dot " +
            (running ? "running" : "stopped");

        $("error-count").textContent =
            data.error_count ?? 0;


    } catch {

        $("status").textContent =
            "Disconnected";

        $("status-dot").className =
            "status-dot stopped";
    }
}

async function loadLogs() {

    try {

        const response =
            await fetch("/api/logs");

        logs =
            await response.json();

        renderLogs();

    } catch {
        // Ignore temporary connection errors.
    }
}

async function clearLogs() {
    const button = $("clear-logs");

    if (!confirm("Clear all logs?")) {
        return;
    }

    try {
        button.disabled = true;

        const response = await fetch("/api/logs/clear", {
            method: "POST"
        });

        if (!response.ok) {
            throw new Error(await response.text());
        }

        logs = [];
        renderLogs();

        $("error-count").textContent = "0";
    } catch (e) {
        showError(e.message);
    } finally {
        button.disabled = false;
    }
}

function renderLogs() {

    const container = $("logs");

    let filtered = logs;

    if (logFilter !== "all") {
        filtered =
            logs.filter(x =>
                x.level === logFilter
            );
    }

    if (!filtered.length) {

        container.innerHTML = `
            <div class="logs-empty">
                No messages
            </div>
        `;

        return;
    }

    container.innerHTML =
        filtered
            .slice()
            .reverse()
            .map(x => {

                const time =
                    new Date(x.time)
                        .toLocaleTimeString();

                return `
                    <div class="log-entry">
                        <span class="log-time">
                            ${esc(time)}
                        </span>

                        <span class="log-level ${x.level}">
                            ${esc(x.level.toUpperCase())}
                        </span>

                        <span class="log-message">
                            ${esc(x.message)}
                        </span>
                    </div>
                `;

            })
            .join("");
}

function showError(message) {

    $("status").textContent =
        "Error";

    $("status-dot").className =
        "status-dot stopped";

    console.error(message);
}

function esc(value) {

    return String(value ?? "")
        .replace(/[&<>"']/g, c => ({
            "&": "&amp;",
            "<": "&lt;",
            ">": "&gt;",
            '"': "&quot;",
            "'": "&#39;"
        }[c]));
}

$("vm-toggle")?.addEventListener("change", () => {
    if (!cfg.output) cfg.output = {};
    if (!cfg.output.victoriametrics) {
        cfg.output.victoriametrics = {};
    }

    const enabled = $("vm-toggle").checked;

    cfg.output.victoriametrics.enabled = enabled;

    if (!enabled) {
        renderVMStatus("Disabled");
    } else {
        renderVMStatus("Disconnected");
        void checkVMHealth();
    }
});

$("vm-address")?.addEventListener("input", () => {
    if (!cfg.output) cfg.output = {};
    if (!cfg.output.victoriametrics) cfg.output.victoriametrics = {};
    cfg.output.victoriametrics.address = $("vm-address").value.trim();
});

$("vm-toggle")?.addEventListener("change", () => {
    if (!cfg.output) cfg.output = {};
    if (!cfg.output.victoriametrics) cfg.output.victoriametrics = {};
    cfg.output.victoriametrics.enabled = $("vm-toggle").checked;
});

$("ws-port")?.addEventListener("input", () => {
    if (!cfg.output) cfg.output = {};
    if (!cfg.output.websocket) cfg.output.websocket = {};
    const port = Number($("ws-port").value);
    if (Number.isInteger(port) && port >= 1 && port <= 65535) {
        cfg.output.websocket.port = port;
    }
});

$("ws-toggle")?.addEventListener("change", () => {
    if (!cfg.output) cfg.output = {};
    if (!cfg.output.websocket) cfg.output.websocket = {};
    cfg.output.websocket.enabled = $("ws-toggle").checked;
    renderWSStatus($("ws-toggle").checked);
    startWebSocket();
});

$("ws-token-generate")?.addEventListener("click", () => {
    const bytes = new Uint8Array(32);
    crypto.getRandomValues(bytes);
    $("ws-token").value = Array.from(bytes, b => b.toString(16).padStart(2, "0")).join("");
});

$("ws-token-visibility")?.addEventListener("click", () => {
    const input = $("ws-token");
    if (!input) return;
    const visible = input.type === "password";
    input.type = visible ? "text" : "password";
    $("ws-token-visibility").textContent = visible ? "Hide" : "Show";
});


$("ws-token")?.addEventListener("input", () => {
    if (!cfg.output) cfg.output = {};
    if (!cfg.output.websocket) cfg.output.websocket = {};
    cfg.output.websocket.token = $("ws-token").value;
});

$("ws-port")?.addEventListener("change", () => {
    if ($("ws-toggle")?.checked) startWebSocket();
});

$("add").onclick =
    addMetric;

$("save").onclick =
    save;

$("modal-close").onclick =
    closeEditor;

$("modal-cancel").onclick =
    closeEditor;

$("modal-save").onclick =
    saveMetric;

$("modal-backdrop")?.addEventListener(
    "click",
    closeEditor
);

$("f-source").addEventListener(
    "change",
    () => {
        renderDynamicFields({});
        updateMemoryNameField();
    }
);

function updateMemoryNameField() {
    const source = $("f-source").value;
    const nameField = $("f-name");
    const selectedMetric = $("f-metric")?.value;

    if (source === "memory") {
        nameField.value = memoryMetricName(selectedMetric);
        nameField.readOnly = true;
    } else if (source === "cpu") {
        nameField.value = cpuMetricName(selectedMetric);
        nameField.readOnly = true;
    } else if (
        ["storage", "directory"].includes(source) ||
        (source === "file" && ["size_mb", "size_gb"].includes(selectedMetric))
    ) {
        nameField.value = `${source}_${selectedMetric}`;
        nameField.readOnly = true;
    } else {
        nameField.readOnly = false;
    }
}

$("dynamic-fields").addEventListener("change", event => {
    if (event.target.id === "f-metric" &&
        ["memory", "cpu", "storage", "directory", "file"].includes($("f-source").value)) {
        updateMemoryNameField();
    }
});

$("clear-logs")?.addEventListener(
    "click",
    clearLogs
);

document.querySelectorAll(".filter")
    .forEach(button => {

        button.addEventListener(
            "click",
            () => {

                document
                    .querySelectorAll(".filter")
                    .forEach(x =>
                        x.classList.remove("active")
                    );

                button.classList.add("active");

                logFilter =
                    button.dataset.level;

                renderLogs();
            }
        );
    });

$("json-toggle").onclick = () => {

    const container =
        $("json-container");

    const button =
        $("json-toggle");

    container.classList.toggle("hidden");

    button.querySelector("span").textContent =
        container.classList.contains("hidden")
            ? "▸"
            : "▾";
};

sendHeartbeat();
setInterval(sendHeartbeat, 5000);

setInterval(updateStatus, 3000);
setInterval(loadLogs, 3000);
setInterval(loadMetricStatuses, 3000);

load();
updateStatus();
loadLogs();
loadMetricStatuses();
startVMHealthChecks();
