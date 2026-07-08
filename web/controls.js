// controls.js — 虚拟按键面板逻辑
(function() {
  // ====== 修饰键状态切换 ======
  const modKeys = {};

  document.querySelectorAll('.mod-key').forEach(btn => {
    const key = btn.dataset.key;
    modKeys[key] = false;

    btn.addEventListener('pointerdown', (e) => {
      e.preventDefault();
      modKeys[key] = !modKeys[key];
      btn.classList.toggle('active', modKeys[key]);

      // 发送按键按下或释放
      wsClient.send({
        type: 'key_press',
        key: key,
        action: modKeys[key] ? 'down' : 'up'
      });
    });
  });

  // ====== 功能键/快捷键宏 ======
  window.sendKey = function(key) {
    wsClient.send({ type: 'key_press', key: key, action: 'tap' });
  };

  window.sendCombo = function(keys) {
    wsClient.send({ type: 'key_combo', keys: keys });
  };

  window.sendText = function() {
    const input = document.getElementById('textInput');
    const text = input.value.trim();
    if (!text) return;

    wsClient.send({ type: 'text_input', text: text });
    input.value = '';
    input.blur();
  };

  // 回车键发送文本
  document.getElementById('textInput').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      sendText();
    }
  });

  // ====== 电源控制 ======
  window.sendPower = function(action) {
    wsClient.send({ type: 'power', action: action });
  };

  // ====== 双击视频区域切换按键面板显示 ======
  const controlPanel = document.getElementById('controlPanel');
  const touchLayer = document.getElementById('touchLayer');
  let panelVisible = true;
  let hideTimer = null;

  // 5秒后自动隐藏面板
  function autoHidePanel() {
    clearTimeout(hideTimer);
    hideTimer = setTimeout(() => {
      if (panelVisible) {
        controlPanel.classList.add('hidden');
        panelVisible = false;
      }
    }, 5000);
  }

  // 双击显示/隐藏面板
  let tapCount = 0;
  let tapTimer = null;
  const DOUBLE_TAP_MS = 300;

  document.addEventListener('touchend', (e) => {
    // 只在触控层区域 (排除按键面板)
    if (e.target.closest('#controlPanel') || e.target.closest('#statusBar')) return;

    tapCount++;
    if (tapCount === 1) {
      tapTimer = setTimeout(() => {
        tapCount = 0;
      }, DOUBLE_TAP_MS);
    } else if (tapCount === 2) {
      clearTimeout(tapTimer);
      tapCount = 0;
      panelVisible = !panelVisible;
      controlPanel.classList.toggle('hidden', !panelVisible);
      if (panelVisible) autoHidePanel();
    }
  });

  // 页面加载后启动自动隐藏
  autoHidePanel();

  // 触摸屏幕时重置计时器
  touchLayer.addEventListener('touchstart', () => {
    if (!panelVisible) {
      controlPanel.classList.remove('hidden');
      panelVisible = true;
    }
    autoHidePanel();
  }, { passive: true });

})();
