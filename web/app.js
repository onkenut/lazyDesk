// app.js — 应用主入口
(function() {
  const statusDot   = document.getElementById('statusDot');
  const statusText  = document.getElementById('statusText');
  const serverIP    = document.getElementById('serverIP');
  const overlay     = document.getElementById('connectOverlay');
  const overlayIP   = document.getElementById('overlayIP');
  const overlayText = overlay ? overlay.querySelector('.overlay-text') : null;

  let connected = false;
  let webrtcWatchdog = null;
  const WEBRTC_TIMEOUT = 12000; // 12s 内必须收到 ontrack

  // 默认地址：当前页面就是服务端
  const defaultHost = window.location.host;
  serverIP.value = defaultHost;
  if (overlayIP) overlayIP.value = defaultHost;

  // ====== WS 消息路由 ======
  wsClient.onMessage = (msg) => {
    if (msg.type === 'answer' || msg.type === 'candidate') {
      if (typeof handleWebRTCSignal === 'function') handleWebRTCSignal(msg);
    }
    if (msg.type === 'error') {
      showError(msg.code + ': ' + msg.message);
    }
  };

  // ====== server_ready → 启动 WebRTC ======
  wsClient.onReady = () => {
    console.log('Server ready, starting WebRTC...');
    startWebRTCWatchdog();
    if (typeof startWebRTC === 'function') startWebRTC();
  };

  function startWebRTCWatchdog() {
    clearTimeout(webrtcWatchdog);
    webrtcWatchdog = setTimeout(() => {
      if (!videoReady && pc && pc.connectionState !== 'connected') {
        showError('视频流超时: WebRTC 连接未能在 '
          + (WEBRTC_TIMEOUT/1000) + 's 内建立。请确认:\n'
          + '1. PC 防火墙允许 UDP 入站\n'
          + '2. 平板和 PC 在同一子网\n'
          + '3. 没有 VPN/代理干扰');
      }
    }, WEBRTC_TIMEOUT);
  }

  function showError(msg) {
    console.error(msg);
    if (overlay) {
      overlay.style.display = 'flex';
      if (overlayText) overlayText.textContent = msg;
    }
    if (statusText) statusText.textContent = '连接失败';
    if (statusDot) statusDot.className = 'disconnected';
  }

  // ====== 连接状态 ======
  wsClient.onStatusChange = (status) => {
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
        if (overlay && wsClient.reconnectAttempts === 0) {
          overlay.style.display = 'flex';
          if (overlayText) overlayText.textContent = '连接断开';
        }
        break;
    }
  };

  // ====== 连接 ======
  window.connect = function() {
    const host = serverIP.value.trim() || defaultHost;
    if (!host) return;

    serverIP.value = host;
    localStorage.setItem('lazyDesk_host', host);
    if (overlayText) overlayText.textContent = '正在连接...';

    if (window.pc) { window.pc.close(); window.pc = null; }
    videoReady = false;
    clearTimeout(webrtcWatchdog);

    wsClient.connect(host);
  };

  // ====== 自动连接: 直接用当前地址 ======
  setTimeout(() => {
    if (!connected) connect();
  }, 400);

  // ====== 状态栏闲置淡出 ======
  const statusBar = document.getElementById('statusBar');
  let idleTimer;
  function resetIdle() {
    if (statusBar) statusBar.classList.remove('idle');
    clearTimeout(idleTimer);
    idleTimer = setTimeout(() => {
      if (statusBar) statusBar.classList.add('idle');
    }, 3000);
  }
  document.addEventListener('touchstart', resetIdle);
  document.addEventListener('pointermove', resetIdle);
  resetIdle();

  // ====== 首次点击 → 解除视频静音 ======
  document.addEventListener('click', function unlockAudio() {
    const video = document.getElementById('remoteVideo');
    if (video) { video.muted = false; video.play().catch(() => {}); }
  }, { once: true });

  // ====== 全局错误处理 ======
  window.onerror = function(msg, src, line) {
    console.error('JS Error:', msg, src, line);
  };
})();
