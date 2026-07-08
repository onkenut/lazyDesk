// touch.js — 触控事件→鼠标动作映射
(function() {
  const touchLayer = document.getElementById('touchLayer');
  if (!touchLayer) return;

  let startX = 0, startY = 0;
  let startTime = 0;
  let isDragging = false;
  let longPressTimer = null;
  const LONG_PRESS_MS = 500; // 与 config.yaml 中保持一致

  // 双指滚动追踪状态
  let lastPinchCenterY = 0;
  let isPinching = false;

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

  // 禁止 Ctrl+滚轮/Ctrl+加号缩放
  document.addEventListener('keydown', e => {
    if (e.ctrlKey && ['+', '-', '=', '0'].includes(e.key)) {
      e.preventDefault();
    }
  });

  // ====== 触控层事件处理 ======
  function getRatio(clientX, clientY) {
    const rect = touchLayer.getBoundingClientRect();
    return {
      x: (clientX - rect.left) / rect.width,
      y: (clientY - rect.top) / rect.height
    };
  }

  function getTouchCount(e) {
    return e.touches.length;
  }

  function getTwoFingerCenter(e) {
    if (e.touches.length < 2) return { x: 0, y: 0 };
    const t0 = e.touches[0];
    const t1 = e.touches[1];
    return {
      x: (t0.clientX + t1.clientX) / 2,
      y: (t0.clientY + t1.clientY) / 2
    };
  }

  function handleTouchStart(e) {
    e.preventDefault();
    const count = getTouchCount(e);

    if (count === 1) {
      const touch = e.touches[0];
      const ratio = getRatio(touch.clientX, touch.clientY);
      startX = ratio.x;
      startY = ratio.y;
      startTime = Date.now();
      isDragging = false;
      isPinching = false;

      // 单指: 发送鼠标位置 + 启动长按计时器
      wsClient.send({ type: 'mouse_move', x: ratio.x, y: ratio.y });

      longPressTimer = setTimeout(() => {
        // 长按触发右键
        wsClient.send({ type: 'mouse_click', button: 'right', action: 'click' });
        isDragging = false; // 长按不触发拖动
      }, LONG_PRESS_MS);
    } else if (count === 2) {
      // 双指: 开始滚动追踪
      clearTimeout(longPressTimer);
      isDragging = false;
      isPinching = true;
      const center = getTwoFingerCenter(e);
      lastPinchCenterY = center.y;
    }
  }

  function handleTouchMove(e) {
    e.preventDefault();
    const count = getTouchCount(e);

    if (count === 1 && !isDragging) {
      const touch = e.touches[0];
      const ratio = getRatio(touch.clientX, touch.clientY);

      // 判断是否开始拖动 (移动超过阈值)
      const dx = Math.abs(ratio.x - startX);
      const dy = Math.abs(ratio.y - startY);
      if (dx > 0.005 || dy > 0.005) {
        isDragging = true;
        clearTimeout(longPressTimer);
      }
    }

    if (count === 1 && isDragging) {
      // 单指拖动 → 鼠标移动
      const touch = e.touches[0];
      const ratio = getRatio(touch.clientX, touch.clientY);
      wsClient.send({ type: 'mouse_move', x: ratio.x, y: ratio.y });
    } else if (count === 2 && isPinching) {
      // 双指滑动 → 滚轮 (追踪两指中心点 Y 轴位移)
      const center = getTwoFingerCenter(e);
      const deltaY = center.y - lastPinchCenterY;

      if (Math.abs(deltaY) > 2) { // 像素阈值，避免抖动
        // 转换为滚轮步数 (≈20px 对应一个滚轮刻度)
        const steps = Math.round(deltaY / 20);
        if (steps !== 0) {
          wsClient.send({ type: 'mouse_scroll', deltaY: steps });
          lastPinchCenterY = center.y;
        }
      }
    }
  }

  function handleTouchEnd(e) {
    e.preventDefault();
    clearTimeout(longPressTimer);

    if (!isDragging && !isPinching && getTouchCount(e.changedTouches) === 1) {
      const elapsed = Date.now() - startTime;
      if (elapsed < LONG_PRESS_MS) {
        // 短按 → 左键点击
        wsClient.send({ type: 'mouse_click', button: 'left', action: 'click' });
      }
    }

    // 如果所有手指都离开，重置状态
    if (e.touches.length === 0) {
      isDragging = false;
      isPinching = false;
    }
  }

  // 绑定事件 (must use { passive: false })
  touchLayer.addEventListener('touchstart', handleTouchStart, { passive: false });
  touchLayer.addEventListener('touchmove', handleTouchMove, { passive: false });
  touchLayer.addEventListener('touchend', handleTouchEnd, { passive: false });
})();
