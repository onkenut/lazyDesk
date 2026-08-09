// render.js — WebRTC 视频渲染到 canvas (含渲染区域记录, 供触控坐标映射)
export function createRenderer(videoEl, canvasEl) {
  let raf = null;
  let running = false;
  let onResizeHandler = null;

  function resize() {
    canvasEl.width = window.innerWidth * (window.devicePixelRatio || 1);
    canvasEl.height = window.innerHeight * (window.devicePixelRatio || 1);
    canvasEl.style.width = '100vw';
    canvasEl.style.height = '100vh';
  }

  function draw() {
    if (!running) return;
    if (videoEl.readyState < 2) {
      raf = requestAnimationFrame(draw);
      return;
    }
    const ctx = canvasEl.getContext('2d');
    const vw = videoEl.videoWidth || 1920;
    const vh = videoEl.videoHeight || 1080;
    const cw = canvasEl.width;
    const ch = canvasEl.height;
    const va = vw / vh;
    const ca = cw / ch;
    let dx, dy, dw, dh;
    if (ca > va) {
      dh = ch; dw = ch * va; dx = (cw - dw) / 2; dy = 0;
    } else {
      dw = cw; dh = cw / va; dx = 0; dy = (ch - dh) / 2;
    }

    ctx.fillStyle = '#000';
    ctx.fillRect(0, 0, cw, ch);
    ctx.drawImage(videoEl, dx, dy, dw, dh);

    // 归一化渲染区域 (0..1), 供触控层坐标映射
    canvasEl._renderRect = { left: dx / cw, top: dy / ch, width: dw / cw, height: dh / ch };
    raf = requestAnimationFrame(draw);
  }

  return {
    start() {
      if (running) return;
      running = true;
      resize();
      if (onResizeHandler) window.removeEventListener('resize', onResizeHandler);
      onResizeHandler = resize;
      window.addEventListener('resize', resize);
      draw();
    },
    stop() {
      running = false;
      if (raf) cancelAnimationFrame(raf);
      raf = null;
      if (onResizeHandler) {
        window.removeEventListener('resize', onResizeHandler);
        onResizeHandler = null;
      }
    },
    renderRect() {
      return canvasEl._renderRect ||
        { left: 0, top: 0, width: 1, height: 1 };
    },
  };
}
