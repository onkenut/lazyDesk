// controls.js — 虚拟按键面板逻辑
(function() {
  // ====== 修饰键状态切换 ======
  const modKeys = {};

  // N7: 重连时重置所有修饰键状态
  window.onConnectionStatusChange = function(status) {
    if (status === 'connected') {
      Object.keys(modKeys).forEach(k => { modKeys[k] = false; });
      document.querySelectorAll('.mod-key').forEach(b => b.classList.remove('active'));
    }
  };

  document.querySelectorAll('.mod-key').forEach(btn => {
    const key = btn.dataset.key;
    modKeys[key] = false;

    btn.addEventListener('pointerdown', (e) => {
      e.preventDefault();
      e.stopPropagation(); // 不触发 touchLayer
      modKeys[key] = !modKeys[key];
      btn.classList.toggle('active', modKeys[key]);

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
    showKeyboard(false);
  };

  // 回车发送
  const textInput = document.getElementById('textInput');
  textInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      sendText();
    }
    e.stopPropagation();
  });

  // ====== 电源控制 ======
  window.sendPower = function(action) {
    wsClient.send({ type: 'power', action: action });
  };

  // ====== 面板显示/隐藏 ======
  const controlPanel = document.getElementById('controlPanel');
  let panelVisible = true;

  window.togglePanel = function() {
    panelVisible = !panelVisible;
    controlPanel.classList.toggle('hidden', !panelVisible);
    // 同步更新切换按钮图标
    const toggleBtn = document.getElementById('togglePanelBtn');
    if (toggleBtn) {
      toggleBtn.textContent = panelVisible ? '▼' : '⌨';
      toggleBtn.style.opacity = panelVisible ? '1' : '0.5';
    }
    // 隐藏/显示文字输入
    if (!panelVisible) showKeyboard(false);
  };

  // 防止按键事件冒泡到 touchLayer
  controlPanel.addEventListener('pointerdown', (e) => {
    e.stopPropagation();
  });
  controlPanel.addEventListener('touchstart', (e) => {
    e.stopPropagation();
  });

  // ====== 文字输入 ======
  function showKeyboard(show) {
    const input = document.getElementById('textInput');
    if (show) {
      input.focus();
      // 移动端需要点击事件触发键盘
      input.click();
    } else {
      input.blur();
    }
  }

  // 点击输入框直接聚焦
  textInput.addEventListener('focus', () => {
    textInput.readOnly = false;
  });

  // ====== 键盘切换按钮 ======
  window.toggleKeyboard = function() {
    const input = document.getElementById('textInput');
    if (document.activeElement === input) {
      input.blur();
    } else {
      // 确保面板可见
      if (!panelVisible) togglePanel();
      input.focus();
      input.click();
    }
  };

  // 初始状态: 面板可见
  controlPanel.classList.remove('hidden');
  panelVisible = true;
})();
