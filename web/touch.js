// touch.js — 触控→鼠标映射 (含 object-fit:contain 黑边补偿)
(function() {
  const layer = document.getElementById('touchLayer');
  const video = document.getElementById('remoteVideo');
  if (!layer) return;

  let sx = 0, sy = 0, st = 0, dragging = false, longTimer = null;
  const LONG = 500;

  // ====== 全局禁止浏览器手势 ======
  document.addEventListener('contextmenu', e => e.preventDefault());
  ['gesturestart','gesturechange','gestureend'].forEach(e =>
    document.addEventListener(e, ev => ev.preventDefault()));

  let lastEnd = 0;
  document.addEventListener('touchend', e => {
    if (Date.now() - lastEnd <= 300) e.preventDefault();
    lastEnd = Date.now();
  }, { passive: false });

  document.addEventListener('keydown', e => {
    if (e.ctrlKey && ['+','-','=','0'].includes(e.key)) e.preventDefault();
  });

  // ====== 计算视频内容区 (补偿 object-fit:contain 黑边) ======
  function videoContentRect() {
    const vw = video.videoWidth  || 1920;
    const vh = video.videoHeight || 1080;
    const cw = layer.clientWidth;
    const ch = layer.clientHeight;
    if (!cw || !ch) return { left:0, top:0, width:1, height:1 };

    const va = vw / vh;
    const ca = cw / ch;
    let w, h, ox, oy;

    if (ca > va) {
      // 容器更宽 → 垂直贴满，水平居中
      h = ch; w = ch * va; ox = (cw - w) / 2; oy = 0;
    } else {
      // 容器更高 → 水平贴满，垂直居中
      w = cw; h = cw / va; ox = 0; oy = (ch - h) / 2;
    }
    return { left: ox, top: oy, width: w, height: h };
  }

  function videoRatio(cx, cy) {
    const r = videoContentRect();
    return {
      x: Math.max(0, Math.min(1, (cx - r.left) / r.width)),
      y: Math.max(0, Math.min(1, (cy - r.top)  / r.height))
    };
  }

  // ====== 触控事件 ======
  function onStart(e) {
    e.preventDefault();
    const t = e.touches[0];
    const r = videoRatio(t.clientX, t.clientY);
    sx = r.x; sy = r.y; st = Date.now(); dragging = false;

    if (e.touches.length === 1) {
      wsClient.send({ type: 'mouse_move', x: r.x, y: r.y });
      longTimer = setTimeout(() => {
        wsClient.send({ type: 'mouse_click', button: 'right', action: 'click' });
      }, LONG);
    } else clearTimeout(longTimer);
  }

  function onMove(e) {
    e.preventDefault();
    if (e.touches.length === 1) {
      const r = videoRatio(e.touches[0].clientX, e.touches[0].clientY);
      if (!dragging && (Math.abs(r.x-sx) > 0.005 || Math.abs(r.y-sy) > 0.005)) {
        dragging = true; clearTimeout(longTimer);
      }
      if (dragging) wsClient.send({ type: 'mouse_move', x: r.x, y: r.y });
    } else if (e.touches.length === 2) {
      clearTimeout(longTimer);
      const r1 = videoRatio(e.touches[0].clientX, e.touches[0].clientY);
      const r2 = videoRatio(e.touches[1].clientX, e.touches[1].clientY);
      const cy = (r1.y + r2.y) / 2;
      if (Math.abs(cy - sy) > 0.008) {
        wsClient.send({ type: 'mouse_scroll', deltaY: cy > sy ? 10 : -10 });
        sy = cy;
      }
    }
  }

  function onEnd(e) {
    e.preventDefault();
    clearTimeout(longTimer);
    if (!dragging && e.changedTouches.length === 1 && Date.now()-st < LONG) {
      wsClient.send({ type: 'mouse_click', button: 'left', action: 'click' });
    }
    dragging = false;
  }

  layer.addEventListener('touchstart', onStart, { passive: false });
  layer.addEventListener('touchmove',  onMove,  { passive: false });
  layer.addEventListener('touchend',   onEnd,   { passive: false });

  // 桌面调试: 鼠标点击
  layer.addEventListener('mousedown', e => {
    const r = videoRatio(e.clientX, e.clientY);
    wsClient.send({ type: 'mouse_move',  x: r.x, y: r.y });
    wsClient.send({ type: 'mouse_click', button: 'left', action: 'click' });
  });
})();
