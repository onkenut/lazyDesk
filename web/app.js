// app.js — 应用主入口 (连接管理 + 状态显示)
(function() {
  const statusDot = document.getElementById('statusDot');
  const statusText = document.getElementById('statusText');
  const serverIP = document.getElementById('serverIP');
  const connectOverlay = document.getElementById('connectOverlay');

  let connected = false;

  // 加载本地存储
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

  // ====== server_ready → 启动 WebRTC ======
  wsClient.onReady = () => {
    console.log('Server ready, initiating WebRTC...');
    if (typeof startWebRTC === 'function') {
      startWebRTC();
    }
  };

  // ====== 连接状态 ======
  wsClient.onStatusChange = (status) => {
    statusDot.className = status;
    switch (status) {
      case 'connecting':
        statusText.textContent = '连接中...';
        if (connectOverlay) {
          connectOverlay.querySelector('.overlay-text').textContent = '正在连接...';
        }
        break;
      case 'connected':
        statusText.textContent = '已连接';
        connected = true;
        if (connectOverlay) {
          connectOverlay.style.display = 'none';
        }
        break;
      case 'disconnected':
        statusText.textContent = '已断开';
        connected = false;
        // 断线后显示连接界面
        if (connectOverlay && wsClient.reconnectAttempts === 0) {
          connectOverlay.style.display = 'flex';
          connectOverlay.querySelector('.overlay-text').textContent = 
            '连接断开，请重新输入 PC IP 地址';
        }
        break;
    }
  };

  // ====== 连接按钮 ======
  window.connect = function() {
    const host = serverIP.value.trim();
    if (!host) {
      alert('请输入 PC 的 IP 地址和端口，例如: 192.168.1.100:8080');
      return;
    }

    localStorage.setItem('lazyDesk_host', host);
    
    if (connectOverlay) {
      connectOverlay.querySelector('.overlay-text').textContent = '正在连接...';
    }
    
    // 重置 WebRTC (如果之前有连接)
    if (window.pc) {
      window.pc.close();
      window.pc = null;
    }
    
    wsClient.connect(host);
  };

  // ====== 回车连接 ======
  serverIP.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      connect();
    }
  });

  // ====== 自动连接 ======
  if (savedHost) {
    setTimeout(() => {
      if (!connected) {
        connect();
      }
    }, 800);
  }

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

  // ====== 首次用户点击画面 → 解除视频静音 ======
  document.addEventListener('click', function unlockAudio() {
    const video = document.getElementById('remoteVideo');
    if (video) {
      video.muted = false;
      video.play().catch(() => {});
    }
  }, { once: true });

})();
