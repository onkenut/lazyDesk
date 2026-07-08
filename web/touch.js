// touch.js — 触控事件→鼠标动作映射
(function() {
  const touchLayer = document.getElementById('touchLayer');
  if (!touchLayer) return;

  let startX = 0, startY = 0;
  let startTime = 0;
  let isDragging = false;
  let longPressTimer = null;
  const LONG_PRESS_MS = 500;

  // ====== 全局阻止浏览器手势 ======
  document.addEventListener('contextmenu', e => e.preventDefault());
  document.addEventListener('gesturestart', e => e.preventDefault());
  document.addEventListener('gesturechange', e => e.preventDefault());
  document.addEventListener('gestureend', e => e.preventDefault());

  // 禁止双击放大 (Android Chrome)
  let lastTouchEnd = 0;
  document.addEventListener('touchend', e => {
    const now = Date.now();
    if (now - lastTouchEnd <= 300) {
      e.preventDefault();
    }
    lastTouchEnd = now;
  }, { passive: false });

  // 禁止 Ctrl+缩放
  document.addEventListener('keydown', e => {
    if (e.ctrlKey && ['+', '-', '=', '0'].includes(e.key)) {
      e.preventDefault();
    }
  });

  // ====== 触控→鼠标映射 ======
  function getRatio(clientX, clientY) {
    const rect = touchLayer.getBoundingClientRect();
    return {
      x: Math.max(0, Math.min(1, (clientX - rect.left) / rect.width)),
      y: Math.max(0, Math.min(1, (clientY - rect.top) / rect.height))
    };
  }

  function handleTouchStart(e) {
    e.preventDefault();
    const touch = e.touches[0];
    const ratio = getRatio(touch.clientX, touch.clientY);
    startX = ratio.x;
    startY = ratio.y;
    startTime = Date.now();
    isDragging = false;

    if (e.touches.length === 1) {
      wsClient.send({ type: 'mouse_move', x: ratio.x, y: ratio.y });

      longPressTimer = setTimeout(() => {
        wsClient.send({ type: 'mouse_click', button: 'right', action: 'click' });
        isDragging = false;
      }, LONG_PRESS_MS);
    } else {
      clearTimeout(longPressTimer);
    }
  }

  function handleTouchMove(e) {
    e.preventDefault();

    if (e.touches.length === 1) {
      const touch = e.touches[0];
      const ratio = getRatio(touch.clientX, touch.clientY);

      if (!isDragging) {
        const dx = Math.abs(ratio.x - startX);
        const dy = Math.abs(ratio.y - startY);
        if (dx > 0.005 || dy > 0.005) {
          isDragging = true;
          clearTimeout(longPressTimer);
        }
      }

      if (isDragging) {
        wsClient.send({ type: 'mouse_move', x: ratio.x, y: ratio.y });
      }
    } else if (e.touches.length === 2) {
      // 双指滑动 → 滚轮
      clearTimeout(longPressTimer);
      const touch1 = e.touches[0];
      const touch2 = e.touches[1];
      const r1 = getRatio(touch1.clientX, touch1.clientY);
      const r2 = getRatio(touch2.clientX, touch2.clientY);
      const centerY = (r1.y + r2.y) / 2;
      
      // 相对于起点的移动方向
      const delta = centerY - startY;
      if (Math.abs(delta) > 0.008) {
        wsClient.send({ type: 'mouse_scroll', deltaY: delta > 0 ? 10 : -10 });
        startY = centerY; // 更新起点
      }
    }
  }

  function handleTouchEnd(e) {
    e.preventDefault();
    clearTimeout(longPressTimer);

    if (!isDragging && e.changedTouches.length === 1) {
      const elapsed = Date.now() - startTime;
      if (elapsed < LONG_PRESS_MS) {
        wsClient.send({ type: 'mouse_click', button: 'left', action: 'click' });
      }
    }
    isDragging = false;
  }

  // 绑定事件
  touchLayer.addEventListener('touchstart', handleTouchStart, { passive: false });
  touchLayer.addEventListener('touchmove', handleTouchMove, { passive: false });
  touchLayer.addEventListener('touchend', handleTouchEnd, { passive: false });

  // 鼠标事件 (桌面调试用)
  touchLayer.addEventListener('mousedown', (e) => {
    const ratio = getRatio(e.clientX, e.clientY);
    wsClient.send({ type: 'mouse_move', x: ratio.x, y: ratio.y });
    wsClient.send({ type: 'mouse_click', button: 'left', action: 'click' });
  });
})();
