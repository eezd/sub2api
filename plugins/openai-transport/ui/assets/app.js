(() => {
  "use strict";

  const bridge = window.Sub2APIBridge;
  const saveButton = document.getElementById("saveButton");
  const testButton = document.getElementById("testButton");
  const reloadButton = document.getElementById("reloadButton");
  const statusMessage = document.getElementById("statusMessage");
  const healthBadge = document.getElementById("healthBadge");
  let statusTimer = 0;

  const fields = {
    client_hello_profile: document.getElementById("clientHelloProfile"),
    proxy_mode: document.getElementById("proxyMode"),
    request_timeout_seconds: document.getElementById("requestTimeout"),
    response_header_timeout_seconds: document.getElementById("responseHeaderTimeout"),
    idle_connection_timeout_seconds: document.getElementById("idleConnectionTimeout"),
    max_idle_connections: document.getElementById("maxIdleConnections"),
    max_idle_connections_per_host: document.getElementById("maxIdleConnectionsPerHost"),
    max_connections_per_host: document.getElementById("maxConnectionsPerHost"),
    extra_headers: document.getElementById("extraHeaders")
  };

  function setBusy(button, busy, busyText) {
    if (!button.dataset.label) button.dataset.label = button.textContent;
    button.disabled = busy;
    button.textContent = busy ? busyText : button.dataset.label;
  }

  function setMessage(message, tone = "neutral") {
    statusMessage.textContent = message;
    statusMessage.dataset.tone = tone;
  }

  function applyConfig(config) {
    fields.client_hello_profile.value = String(config.client_hello_profile || "nodejs_24");
    fields.proxy_mode.value = String(config.proxy_mode || "inherit");
    fields.request_timeout_seconds.value = Number(config.request_timeout_seconds ?? 0);
    fields.response_header_timeout_seconds.value = Number(config.response_header_timeout_seconds ?? 60);
    fields.idle_connection_timeout_seconds.value = Number(config.idle_connection_timeout_seconds ?? 90);
    fields.max_idle_connections.value = Number(config.max_idle_connections ?? 100);
    fields.max_idle_connections_per_host.value = Number(config.max_idle_connections_per_host ?? 20);
    fields.max_connections_per_host.value = Number(config.max_connections_per_host ?? 0);
    fields.extra_headers.value = JSON.stringify(config.extra_headers || {}, null, 2);
  }

  function readInteger(field, name) {
    const value = Number(field.value);
    if (!Number.isInteger(value)) throw new Error(`${name} 必须是整数`);
    return value;
  }

  function collectConfig() {
    let extraHeaders;
    try {
      extraHeaders = JSON.parse(fields.extra_headers.value.trim() || "{}");
    } catch {
      throw new Error("附加请求头必须是有效 JSON");
    }
    if (!extraHeaders || Array.isArray(extraHeaders) || typeof extraHeaders !== "object") {
      throw new Error("附加请求头必须是 JSON 对象");
    }
    for (const [name, value] of Object.entries(extraHeaders)) {
      if (typeof value !== "string") throw new Error(`请求头 ${name} 的值必须是字符串`);
    }
    return {
      client_hello_profile: fields.client_hello_profile.value,
      proxy_mode: fields.proxy_mode.value,
      request_timeout_seconds: readInteger(fields.request_timeout_seconds, "请求总超时"),
      response_header_timeout_seconds: readInteger(fields.response_header_timeout_seconds, "响应头超时"),
      idle_connection_timeout_seconds: readInteger(fields.idle_connection_timeout_seconds, "空闲连接超时"),
      max_idle_connections: readInteger(fields.max_idle_connections, "最大空闲连接"),
      max_idle_connections_per_host: readInteger(fields.max_idle_connections_per_host, "每主机最大空闲连接"),
      max_connections_per_host: readInteger(fields.max_connections_per_host, "每主机最大连接"),
      extra_headers: extraHeaders
    };
  }

  async function loadConfig() {
    setBusy(reloadButton, true, "载入中…");
    try {
      const message = await bridge.request("config.load");
      applyConfig(message.config || {});
      setMessage("已载入当前配置");
    } catch (error) {
      setMessage(error.message || "载入配置失败", "error");
    } finally {
      setBusy(reloadButton, false, "");
    }
  }

  function parseStatusJSON(raw) {
    if (typeof raw !== "string" || raw.trim() === "") return {};
    try {
      const value = JSON.parse(raw);
      return value && typeof value === "object" && !Array.isArray(value) ? value : {};
    } catch {
      return {};
    }
  }

  function showStatus(result) {
    const status = parseStatusJSON(result.status_json);
    healthBadge.textContent = result.healthy ? "运行正常" : "未运行";
    healthBadge.className = result.healthy ? "health health--ok" : "health health--error";
    document.getElementById("statusProfile").textContent = status.client_hello_profile || "—";
    document.getElementById("statusActive").textContent = String(status.active_requests ?? "—");
    document.getElementById("statusRequests").textContent = String(status.requests_total ?? "—");
    document.getElementById("statusPool").textContent = String(status.transport_pool_size ?? "—");
    if (!result.healthy && result.message) setMessage(result.message, "error");
  }

  async function refreshStatus() {
    if (document.hidden) return;
    try {
      const message = await bridge.request("plugin.status", {}, 10000);
      showStatus(message.result || {});
    } catch (error) {
      healthBadge.textContent = "状态不可用";
      healthBadge.className = "health health--error";
      setMessage(error.message || "读取状态失败", "error");
    }
  }

  saveButton.addEventListener("click", async () => {
    setBusy(saveButton, true, "保存中…");
    try {
      const config = collectConfig();
      const message = await bridge.request("config.save", { config });
      applyConfig(message.config || config);
      setMessage("配置已保存并原子应用", "success");
      await refreshStatus();
    } catch (error) {
      setMessage(error.message || "保存失败", "error");
    } finally {
      setBusy(saveButton, false, "");
    }
  });

  testButton.addEventListener("click", async () => {
    setBusy(testButton, true, "测试中…");
    try {
      const message = await bridge.request("config.test", {}, 35000);
      const result = message.result || {};
      setMessage(`${result.message || "测试完成"}${Number.isFinite(result.latency_ms) ? ` · ${result.latency_ms} ms` : ""}`, message.ok ? "success" : "error");
      if (result.status_json) showStatus({ healthy: true, status_json: result.status_json });
    } catch (error) {
      setMessage(error.message || "测试失败", "error");
    } finally {
      setBusy(testButton, false, "");
    }
  });

  reloadButton.addEventListener("click", loadConfig);
  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) refreshStatus();
  });

  const observer = new ResizeObserver(() => {
    bridge.resize(Math.ceil(document.documentElement.scrollHeight + 8));
  });
  observer.observe(document.body);

  Promise.all([loadConfig(), refreshStatus()]).finally(() => {
    bridge.ready();
    bridge.resize(Math.ceil(document.documentElement.scrollHeight + 8));
    statusTimer = window.setInterval(refreshStatus, 5000);
  });

  window.addEventListener("beforeunload", () => {
    if (statusTimer) window.clearInterval(statusTimer);
    observer.disconnect();
  });
})();
