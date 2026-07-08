// touch.js — 触控事件→鼠标动作映射
(function() {
  const touchLayer = document.getElementById('touchLayer');
  if (!touchLayer) return;

  let startX = 0, startY = 0;
  let startTime = 0;
  let isDragging = false;
  let longPressTimer = null;
  const LONG_PRESS_MS = 500; // 与 config.yaml 中保持一致

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

  function handleTouchStart(e) {
    e.preventDefault();
    const touch = e.touches[0];
    const ratio = getRatio(touch.clientX, touch.clientY);
    startX = ratio.x;
    startY = ratio.y;
    startTime = Date.now();
    isDragging = false;

    if (getTouchCount(e) === 1) {
      // 单指: 发送鼠标位置 + 启动长按计时器
      wsClient.send({ type: 'mouse_move', x: ratio.x, y: ratio.y });

      longPressTimer = setTimeout(() => {
        // 长按触发右键
        wsClient.send({ type: 'mouse_click', button: 'right', action: 'click' });
        isDragging = false; // 长按不触发拖动
      }, LONG_PRESS_MS);
    } else if (getTouchCount(e) === 2) {
      // 双指: 记录起始位置用于滚轮计算
      clearTimeout(longPressTimer);
    }
  }

  function handleTouchMove(e) {
    e.preventDefault();

    if (getTouchCount(e) === 1 && !isDragging) {
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

    if (getTouchCount(e) === 1 && isDragging) {
      // 单指拖动 → 鼠标移动
      const touch = e.touches[0];
      const ratio = getRatio(touch.clientX, touch.clientY);
      wsClient.send({ type: 'mouse_move', x: ratio.x, y: ratio.y });
    } else if (getTouchCount(e) === 2) {
      // 双指滑动 → 滚轮
      const touch0 = e.touches[0];
      const touch1 = e.touches[1];
      const ratio0 = getRatio(touch0.clientX, touch0.clientY);
      const ratio1 = getRatio(touch1.clientX, touch1.clientY);

      // 取两指中心点
      const centerY = (ratio0.y + ratio1.y) / 2;
      const deltaY = centerY - ((startY + (getTouchCount(e) === 2 ? (ratio0.y + ratio1.y) / 2 : startY)));

      // 发送滚轮事件 (按 Y 轴方向)
      if (Math.abs(centerY - startY) > 0.01) {
        const scrollSteps = Math.round((centerY - startY) * 100);
        if (scrollSteps !== 0) {
          wsClient.send({ type: 'mouse_scroll', deltaY: scrollSteps > 0 ? 10 : -10 });
        }
      }
    }
  }

  function handleTouchEnd(e) {
    e.preventDefault();
    clearTimeout(longPressTimer);

    if (!isDragging && getTouchCount(e.changedTouches) === 1) {
      const elapsed = Date.now() - startTime;
      if (elapsed < LONG_PRESS_MS) {
        // 短按 → 左键点击
        wsClient.send({ type: 'mouse_click', button: 'left', action: 'click' });
      }
    }

    isDragging = false;
  }

  // 绑定事件 (must use { passive: false })
  touchLayer.addEventListener('touchstart', handleTouchStart, { passive: false });
  touchLayer.addEventListener('touchmove', handleTouchMove, { passive: false });
  touchLayer.addEventListener('touchend', handleTouchEnd, { passive: false });
})();
