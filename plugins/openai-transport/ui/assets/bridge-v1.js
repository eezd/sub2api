(() => {
  "use strict";

  const source = "sub2api-plugin-ui";
  const hostSource = "sub2api-plugin-host";
  const token = new URLSearchParams(window.location.hash.slice(1)).get("bridge_token") || "";
  const pending = new Map();
  let sequence = 0;

  function post(type, payload = {}) {
    window.parent.postMessage({ source, bridge_token: token, type, ...payload }, "*");
  }

  function request(type, payload = {}, timeoutMs = 30000) {
    const requestId = `${Date.now().toString(36)}-${(++sequence).toString(36)}`;
    return new Promise((resolve, reject) => {
      const timeout = window.setTimeout(() => {
        pending.delete(requestId);
        reject(new Error(`${type} 请求超时`));
      }, timeoutMs);
      pending.set(requestId, { resolve, reject, timeout, type });
      post(type, { ...payload, request_id: requestId });
    });
  }

  window.addEventListener("message", (event) => {
    if (event.source !== window.parent) return;
    const message = event.data;
    if (!message || message.source !== hostSource || message.bridge_token !== token) return;
    const requestId = typeof message.request_id === "string" ? message.request_id : "";
    const entry = pending.get(requestId);
    if (!entry || message.type !== `${entry.type}.result`) return;
    window.clearTimeout(entry.timeout);
    pending.delete(requestId);
    if (message.ok) entry.resolve(message);
    else entry.reject(new Error(typeof message.error === "string" ? message.error : "宿主拒绝请求"));
  });

  window.addEventListener("beforeunload", () => {
    for (const entry of pending.values()) {
      window.clearTimeout(entry.timeout);
      entry.reject(new Error("插件页面已关闭"));
    }
    pending.clear();
  });

  window.Sub2APIBridge = Object.freeze({
    request,
    ready: () => post("sub2api.plugin.ready"),
    resize: (height) => post("ui.resize", { height }),
    notify: (level, message) => post("ui.notify", { level, message })
  });
})();
