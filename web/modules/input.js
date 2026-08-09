// input.js — 触控映射 + 虚拟按键面板
import { wsClient } from './ws.js';

export function initInput(renderer) {
  const layer = document.getElementById('touchLayer');

  // ====== 禁止浏览器手势 ======
  document.addEventListener('contextmenu', (e) => e.preventDefault());
  ['gesturestart', 'gesturechange', 'gestureend'].forEach((ev) =>
    document.addEventListener(ev, (e) => e.preventDefault()));
  let lastEnd = 0;
  document.addEventListener('touchend', (e) => {
    if (Date.now() - lastEnd <= 300) e.preventDefault();
    lastEnd = Date.now();
  }, { passive: false });
  document.addEventListener('keydown', (e) => {
    if (e.ctrlKey && ['+', '-', '=', '0'].includes(e.key)) e.preventDefault();
  });

  // ====== 坐标映射: 触控点 → 0..1 视频比例 ======
  function videoRatio(cx, cy) {
    const r = renderer.renderRect();
    const rect = layer.getBoundingClientRect();
    const rx = (cx - rect.left) / rect.width;
    const ry = (cy - rect.top) / rect.height;
    return {
      x: Math.max(0, Math.min(1, (rx - r.left) / r.width)),
      y: Math.max(0, Math.min(1, (ry - r.top) / r.height)),
    };
  }

  // ====== 触控事件 (单指=鼠标, 长按=右键, 双指=滚轮) ======
  const LONG = 500;
  let sx = 0, sy = 0, st = 0, dragging = false, isTwoFinger = false, longTimer = null;
  let lastMove = 0, lastScroll = 0;
  const MOVE_THROTTLE = 16, SCROLL_THROTTLE = 50;

  function onStart(e) {
    e.preventDefault();
    const t = e.touches[0];
    const r = videoRatio(t.clientX, t.clientY);
    sx = r.x; sy = r.y; st = Date.now(); dragging = false;
    if (e.touches.length === 1) {
      isTwoFinger = false;
      wsClient.send({ type: 'mouse_move', x: r.x, y: r.y });
      longTimer = setTimeout(() => {
        wsClient.send({ type: 'mouse_click', button: 'right', action: 'click' });
      }, LONG);
    } else {
      isTwoFinger = true;
      clearTimeout(longTimer);
    }
  }

  function onMove(e) {
    e.preventDefault();
    const now = Date.now();
    if (now - lastMove < MOVE_THROTTLE) return;
    lastMove = now;

    if (e.touches.length === 1) {
      const r = videoRatio(e.touches[0].clientX, e.touches[0].clientY);
      if (!dragging && (Math.abs(r.x - sx) > 0.005 || Math.abs(r.y - sy) > 0.005)) {
        dragging = true;
        clearTimeout(longTimer);
      }
      if (dragging) wsClient.send({ type: 'mouse_move', x: r.x, y: r.y });
    } else if (e.touches.length === 2) {
      clearTimeout(longTimer);
      if (now - lastScroll < SCROLL_THROTTLE) return;
      lastScroll = now;
      const r1 = videoRatio(e.touches[0].clientX, e.touches[0].clientY);
      const r2 = videoRatio(e.touches[1].clientX, e.touches[1].clientY);
      const cy = (r1.y + r2.y) / 2;
      if (Math.abs(cy - sy) > 0.015) {
        wsClient.send({ type: 'mouse_scroll', deltaY: cy > sy ? 10 : -10 });
        sy = cy;
      }
    }
  }

  function onEnd(e) {
    e.preventDefault();
    clearTimeout(longTimer);
    if (!dragging && !isTwoFinger && e.changedTouches.length === 1 && Date.now() - st < LONG) {
      wsClient.send({ type: 'mouse_click', button: 'left', action: 'click' });
    }
    if (e.touches.length === 0) { isTwoFinger = false; dragging = false; }
  }

  layer.addEventListener('touchstart', onStart, { passive: false });
  layer.addEventListener('touchmove', onMove, { passive: false });
  layer.addEventListener('touchend', onEnd, { passive: false });

  // 桌面调试: 鼠标点击
  layer.addEventListener('mousedown', (e) => {
    const r = videoRatio(e.clientX, e.clientY);
    wsClient.send({ type: 'mouse_move', x: r.x, y: r.y });
    wsClient.send({ type: 'mouse_click', button: 'left', action: 'click' });
  });

  // ====== 虚拟按键面板 ======
  const controlPanel = document.getElementById('controlPanel');
  const modKeys = {};

  // 修饰键切换
  document.querySelectorAll('.mod-key').forEach((btn) => {
    const key = btn.dataset.key;
    modKeys[key] = false;
    btn.addEventListener('pointerdown', (e) => {
      e.preventDefault();
      e.stopPropagation();
      modKeys[key] = !modKeys[key];
      btn.classList.toggle('active', modKeys[key]);
      wsClient.send({ type: 'key_press', key, action: modKeys[key] ? 'down' : 'up' });
    });
  });

  // 功能键
  document.querySelectorAll('[data-sendkey]').forEach((btn) => {
    btn.addEventListener('click', () => {
      wsClient.send({ type: 'key_press', key: btn.dataset.sendkey, action: 'tap' });
    });
  });

  // 组合键宏
  document.querySelectorAll('[data-combo]').forEach((btn) => {
    btn.addEventListener('click', () => {
      wsClient.send({ type: 'key_combo', keys: btn.dataset.combo.split(',') });
    });
  });

  // 电源
  document.querySelectorAll('[data-power]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const action = btn.dataset.power;
      if (action === 'shutdown' && !confirm('确定关机?')) return;
      wsClient.send({ type: 'power', action });
    });
  });

  // 文本输入
  const textInput = document.getElementById('textInput');
  function sendText() {
    const text = textInput.value.trim();
    if (!text) return;
    wsClient.send({ type: 'text_input', text });
    textInput.value = '';
    textInput.blur();
  }
  document.getElementById('textSendBtn').addEventListener('click', sendText);
  textInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); sendText(); }
    e.stopPropagation();
  });

  // 面板显示/隐藏
  let panelVisible = true;
  document.getElementById('togglePanelBtn').addEventListener('click', () => {
    panelVisible = !panelVisible;
    controlPanel.classList.toggle('hidden', !panelVisible);
    const btn = document.getElementById('togglePanelBtn');
    btn.textContent = panelVisible ? '▼' : '⌨';
    btn.style.opacity = panelVisible ? '1' : '0.5';
    if (!panelVisible) textInput.blur();
  });

  document.getElementById('keyboardBtn').addEventListener('click', () => {
    if (document.activeElement === textInput) {
      textInput.blur();
    } else {
      if (!panelVisible) {
        panelVisible = true;
        controlPanel.classList.remove('hidden');
        document.getElementById('togglePanelBtn').textContent = '▼';
      }
      textInput.focus();
      textInput.click();
    }
  });

  // 面板事件不冒泡到触控层
  controlPanel.addEventListener('pointerdown', (e) => e.stopPropagation());
  controlPanel.addEventListener('touchstart', (e) => e.stopPropagation());

  // 重连时重置修饰键
  window.addEventListener('lazydesk:conn', (e) => {
    if (e.detail === 'connected') {
      Object.keys(modKeys).forEach((k) => { modKeys[k] = false; });
      document.querySelectorAll('.mod-key').forEach((b) => b.classList.remove('active'));
    }
  });

  return { resetMods: () => {} };
}
