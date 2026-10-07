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
    memory: "Memory",
    storage: "Storage",
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

    (cfg.modbus?.controllers || []).forEach((controller, pi) => {
        (controller.registers || []).forEach((m, i) => {
            out.push({
                ...m,
                key: `modbus:${controller.name}:${i}`,
                _kind: "modbus",
                _pi: pi,
                _i: i,
                _controller: controller.name
            });
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
                <td colspan="7" class="empty">
                    No metrics configured
                </td>
            </tr>
        `;
        return;
    }

    rows.innerHTML = metrics.map((m, i) => {

        const enabled = m.enabled !== false;

        const source =
            m._kind === "modbus"
                ? "Modbus TCP"
                : sourceNames[m.source] || m.source || "—";

        const metric =
            m.metric ||
            m.type ||
            "—";

        const details = metricDetails(m);

        return `
            <tr>
                <td>
                    <input
                        type="checkbox"
                        ${enabled ? "checked" : ""}
                        onchange="toggleMetric(${i}, this.checked)"
                    >
                </td>

                <td>
                    <span class="metric-status ${statusClass(m.key, enabled)}">
                        <span></span>
                        ${statusText(m.key, enabled)}
                    </span>
                </td>

                <td class="metric-name">
                    ${esc(m.name)}
                </td>

                <td>
                    <span class="source-badge">
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

function metricDetails(m) {
    const address = m.address ?? "—";
    const fn = m.function || "holding";

    const device =
        m.device_address
            ? `${m.device_address}:${m.device_port || 502}`
            : "";

    return device
        ? `${device} · ${fn} · ${address}`
        : `${fn} · ${address}`;
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
        cfg.modbus.controllers[m._pi].registers[m._i].enabled = value;
    }

    render();
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
        cfg.modbus.controllers[m._pi].registers.splice(m._i, 1);
    }

    render();
}

function editMetric(index) {
    const m = allMetrics()[index];

    if (!m) return;

    editing = {
        kind: m._kind,
        index: m._i,
        controllerIndex: m._pi
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

    renderDynamicFields(m);

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
                    "load1",
                    "load5",
                    "load15"
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
                [
                    "used",
                    "available",
                    "free",
                    "total",
                    "used_percent"
                ],
                m.metric || "used_percent"
            )}
        `;

        return;
    }

    if (source === "storage") {

        container.innerHTML = `
            ${inputField(
                "Mount",
                "f-path",
                m.path || "/"
            )}

            ${selectField(
                "Metric",
                "f-metric",
                [
                    "used",
                    "free",
                    "total",
                    "used_percent",
                    "free_percent"
                ],
                m.metric || "used_percent"
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

        container.innerHTML = `
            ${inputField(
                "File Path",
                "f-path",
                m.path || ""
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

    if (source === "modbus") {

        container.innerHTML = renderModbusFields(m);

        return;
    }
}

function renderModbusFields(m) {

    const controller =
        cfg.modbus?.controllers?.[m._pi];

    let endpoint = "";

    if (m.device_address) {
        endpoint =
            `${m.device_address}:${m.device_port || 502}`;
    } else if (controller?.address) {
        endpoint = controller.address;
    }

    let unitID =
        m.unit_id || "";

    if (!unitID && controller?.unit_id) {
        unitID = controller.unit_id;
    }

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

    if (source === "modbus") {

        if (!saveModbusMetric(name)) {
            return;
        }

    } else {

        saveSystemMetric(name, source);
    }

    closeEditor();
    render();
}

function saveSystemMetric(name, source) {

    const metric = {
        enabled: true,
        name,
        source
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

        cfg.system.metrics[editing.index] = metric;

    } else {

        cfg.system.metrics.push(metric);
    }
}

function saveModbusMetric(name) {

    if (!cfg.modbus) {
        cfg.modbus = {
            controllers: []
        };
    }

    // Keep the existing controller container internally for
    // compatibility. The user does not select it in the UI.
    if (!cfg.modbus.controllers.length) {
        cfg.modbus.controllers.push({
            name: "Modbus TCP",
            address: "127.0.0.1:502",
            unit_id: 1,
            registers: []
        });
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
    // The backend stores address and port separately.
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
        name,
        device_address: deviceAddress,
        device_port: devicePort,
        unit_id: unitID,
        address,
        function: $("f-function").value,
        type: $("f-type").value,
        offset: Number($("f-offset").value) || 0
    };

    const controllerIndex =
        editing?.controllerIndex ?? 0;

    const controller =
        cfg.modbus.controllers[controllerIndex];

    if (!controller) {
        alert("Modbus configuration error");
        return false;
    }

    if (!controller.registers) {
        controller.registers = [];
    }

    if (editing && editing.kind === "modbus") {

        const old =
            controller.registers[editing.index];

        metric.enabled =
            old?.enabled !== false;

        // Preserve existing labels from older configurations.
        if (old?.labels) {
            metric.labels = old.labels;
        }

        // Preserve hidden compatibility fields.
        metric.byte_order =
            old?.byte_order || "big";

        metric.word_order =
            old?.word_order || "big";

        controller.registers[editing.index] =
            metric;

    } else {

        controller.registers.push(metric);
    }

    return true;
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

    $("vm-toggle-label").textContent =
        enabled ? "ON" : "OFF";

    if (!enabled) {
        $("vm-status").className = "vm-status disabled";
        $("vm-status-text").textContent = "Disabled";
    } else {
        $("vm-status").className = "vm-status waiting";
        $("vm-status-text").textContent = "Waiting";
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
    () => renderDynamicFields({})
);

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
