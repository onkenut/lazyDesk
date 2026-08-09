// ui.js — 状态栏 / 连接遮罩 / 统计 / 事件提示
const $ = (id) => document.getElementById(id);

export const ui = {
  _watchdog: null,
  _WEBRTC_TIMEOUT: 12000,

  // 连接状态栏
  setStatus(text, cls) {
    const t = $('statusText'), d = $('statusDot');
    if (t) t.textContent = text;
    if (d) d.className = cls || '';
  },

  // 显示错误 (遮罩 + 状态栏)
  showError(msg) {
    console.error(msg);
    const overlay = $('connectOverlay');
    const text = $('overlayText');
    if (overlay) overlay.style.display = 'flex';
    if (text) text.textContent = msg;
    this.setStatus('连接失败', 'disconnected');
  },

  // 遮罩控制
  showOverlay(msg) {
    const overlay = $('connectOverlay');
    if (overlay) overlay.style.display = 'flex';
    const text = $('overlayText');
    if (text) text.textContent = msg || '正在连接...';
  },

  hideOverlay() {
    const overlay = $('connectOverlay');
    if (overlay) overlay.style.display = 'none';
  },

  // 更新统计 (来自服务端 stats 消息)
  updateStats(s) {
    const el = $('statsText');
    if (!el) return;
    const fps = Math.round(s.fps || 0);
    const parts = [];
    if (fps > 0) parts.push(`${fps}fps`);
    if (s.clients > 1) parts.push(`${s.clients}客户端`);
    if (s.dropped > 0) parts.push(`丢帧${s.dropped}`);
    if (s.audio === false) parts.push('无音频');
    el.textContent = parts.length ? ' | ' + parts.join(' ') : '';
  },

  // 采集事件提示 (toast)
  onEvent(ev) {
    const toast = $('eventToast');
    if (!toast) return;
    toast.textContent = ev.message || ev.event;
    toast.classList.remove('hidden');
    clearTimeout(this._toastTimer);
    this._toastTimer = setTimeout(() => toast.classList.add('hidden'), 4000);
  },

  // 启动 WebRTC 超时看门狗
  startWatchdog(onTimeout) {
    clearTimeout(this._watchdog);
    this._watchdog = setTimeout(onTimeout, this._WEBRTC_TIMEOUT);
  },

  clearWatchdog() {
    clearTimeout(this._watchdog);
  },

  // 状态栏闲置淡出
  initIdleFade() {
    const bar = $('statusBar');
    let timer;
    const reset = () => {
      if (bar) bar.classList.remove('idle');
      clearTimeout(timer);
      timer = setTimeout(() => bar && bar.classList.add('idle'), 3000);
    };
    document.addEventListener('touchstart', reset);
    document.addEventListener('pointermove', reset);
    reset();
  },
};
