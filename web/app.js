// app.js — 应用主入口 (连接管理 + 状态显示 + 事件路由)
(function() {
  const statusDot = document.getElementById('statusDot');
  const statusText = document.getElementById('statusText');
  const serverIP = document.getElementById('serverIP');
  const remoteVideo = document.getElementById('remoteVideo');

  // 默认读取 localStorage 保存的服务器地址
  const savedHost = localStorage.getItem('lazyDesk_host');
  if (savedHost) {
    serverIP.value = savedHost;
  }

  // ====== WebSocket 消息路由 ======
  wsClient.onMessage = (msg) => {
    // WebRTC 信令消息
    if (msg.type === 'answer' || msg.type === 'candidate') {
      if (typeof handleWebRTCSignal === 'function') {
        handleWebRTCSignal(msg);
      }
    }
  };

  // ====== 连接状态更新 ======
  wsClient.onStatusChange = (status) => {
    statusDot.className = status;
    switch (status) {
      case 'connecting':
        statusText.textContent = '连接中...';
        break;
      case 'connected':
        statusText.textContent = '已连接';
        // 隐藏连接遮罩 (如果有)
        const overlay = document.getElementById('connectOverlay');
        if (overlay) overlay.style.display = 'none';
        break;
      case 'disconnected':
        statusText.textContent = '已断开';
        break;
    }
  };

  // ====== 连接按钮 ======
  window.connect = function() {
    const host = serverIP.value.trim();
    if (!host) return;

    // 保存到 localStorage
    localStorage.setItem('lazyDesk_host', host);

    wsClient.connect(host);
  };

  // ====== 状态栏闲置后淡出 ======
  const statusBar = document.getElementById('statusBar');
  let idleTimer = null;

  function resetIdleTimer() {
    statusBar.classList.remove('idle');
    clearTimeout(idleTimer);
    idleTimer = setTimeout(() => {
      statusBar.classList.add('idle');
    }, 3000);
  }

  document.addEventListener('touchstart', resetIdleTimer);
  document.addEventListener('pointermove', resetIdleTimer);
  resetIdleTimer();

  // ====== 首次用户点击页面时解除视频静音 (绕过 autoplay 策略) ======
  document.addEventListener('click', function unlockAudio() {
    if (remoteVideo) {
      remoteVideo.muted = false;
      remoteVideo.play().catch(() => {});
    }
  }, { once: true });

  // ====== 页面加载后自动连接 (如已保存地址) ======
  if (savedHost) {
    // 延迟一下等页面初始化完成
    setTimeout(() => {
      wsClient.connect(savedHost);
    }, 500);
  }

})();
