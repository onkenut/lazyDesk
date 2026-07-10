// app.js — 应用主入口
(function() {
  var statusDot   = document.getElementById('statusDot');
  var statusText  = document.getElementById('statusText');
  var overlay     = document.getElementById('connectOverlay');
  var overlayIP   = document.getElementById('overlayIP');
  var overlayText = overlay ? overlay.querySelector('.overlay-text') : null;

  var connected = false;
  var webrtcWatchdog = null;
  var WEBRTC_TIMEOUT = 12000;

  function safeVal(el, v) { if (el) el.value = v; }

  var defaultHost = window.location.host;
  safeVal(overlayIP, defaultHost);
  if (overlayText) overlayText.textContent = '正在连接 ' + defaultHost + '...';

  // ====== WS 消息路由 ======
  wsClient.onMessage = function(msg) {
    if (msg.type === 'answer' || msg.type === 'candidate') {
      if (typeof handleWebRTCSignal === 'function') handleWebRTCSignal(msg);
    }
    if (msg.type === 'error') showError(msg.code + ': ' + msg.message);
  };

  wsClient.onReady = function() {
    console.log('Server ready, starting WebRTC...');
    startWatchdog();
    if (typeof startWebRTC === 'function') startWebRTC();
  };

  function startWatchdog() {
    clearTimeout(webrtcWatchdog);
    webrtcWatchdog = setTimeout(function() {
      if (!videoReady && window.pc && window.pc.connectionState !== 'connected') {
        showError('超时: 请确认防火墙允许UDP、平板与PC同子网、无VPN');
      }
    }, WEBRTC_TIMEOUT);
  }

  function showError(msg) {
    console.error(msg);
    if (overlay) overlay.style.display = 'flex';
    if (overlayText) {
      // 保留 span，只替换文本节点，不破坏 overlayHost 子元素
      while (overlayText.firstChild) overlayText.removeChild(overlayText.firstChild);
      overlayText.appendChild(document.createTextNode(msg));
    }
    if (statusText) statusText.textContent = '连接失败';
    if (statusDot) statusDot.className = 'disconnected';
  }

  wsClient.onStatusChange = function(status) {
    if (statusDot) statusDot.className = status;
    switch (status) {
      case 'connecting':
        if (statusText) statusText.textContent = '连接中...';
        if (overlayText) overlayText.textContent = '正在连接...';
        break;
      case 'connected':
        connected = true;
        if (statusText) statusText.textContent = '已连接';
        if (overlay) overlay.style.display = 'none';
        break;
      case 'disconnected':
        connected = false;
        if (statusText) statusText.textContent = '已断开';
        if (overlay) overlay.style.display = 'flex';
        if (overlayText) overlayText.textContent = '连接断开';
        break;
    }
    // N7: 重连时通知 controls.js 重置修饰键状态
    if (typeof onConnectionStatusChange === 'function') {
      onConnectionStatusChange(status);
    }
  };

  window.connect = function() {
    var host = (overlayIP && overlayIP.value.trim()) || defaultHost;
    if (!host) return;
    safeVal(overlayIP, host);
    localStorage.setItem('lazyDesk_host', host);
    if (overlayText) overlayText.textContent = '正在连接...';
    if (window.pc) { window.pc.close(); window.pc = null; }
    videoReady = false;
    clearTimeout(webrtcWatchdog);
    wsClient.connect(host);
  };

  setTimeout(function() { if (!connected) window.connect(); }, 200); // I6: 减少重连延迟

  // 状态栏闲置淡出
  var statusBar = document.getElementById('statusBar'), idleTimer;
  function resetIdle() {
    if (statusBar) statusBar.classList.remove('idle');
    clearTimeout(idleTimer);
    idleTimer = setTimeout(function() {
      if (statusBar) statusBar.classList.add('idle');
    }, 3000);
  }
  document.addEventListener('touchstart', resetIdle);
  document.addEventListener('pointermove', resetIdle);
  resetIdle();

  // 首次点击播放由 webrtc.js ontrack autoplay fallback 处理，此处不再重复
})();
