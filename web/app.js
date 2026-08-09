// app.js — 应用入口 (ESM)
import { wsClient } from './modules/ws.js';
import { webrtc } from './modules/webrtc.js';
import { createRenderer } from './modules/render.js';
import { initInput } from './modules/input.js';
import { ui } from './modules/ui.js';

const defaultHost = window.location.host;
const overlayIP = document.getElementById('overlayIP');
const overlayHost = document.getElementById('overlayHost');

// ====== 初始化 ======
overlayIP.value = defaultHost;
if (overlayHost) overlayHost.textContent = defaultHost;

const renderer = createRenderer(
  document.getElementById('remoteVideo'),
  document.getElementById('remoteCanvas'),
);
initInput(renderer);
ui.initIdleFade();

// ====== WS 回调 ======
wsClient.onReady = () => {
  console.log('Server ready, starting WebRTC...');
  ui.startWatchdog(() => {
    if (!webrtc.videoReady && webrtc.pc && webrtc.pc.connectionState !== 'connected') {
      ui.showError('连接超时: 请确认 1) PC 防火墙放行 UDP 2) 平板与 PC 同一局域网 3) 无 VPN 4) 浏览器未开启"隐藏本地IP"');
    }
  });
  webrtc.start(renderer);
};

wsClient.onMessage = (msg) => {
  if (msg.type === 'answer' || msg.type === 'candidate') {
    webrtc.handleSignal(msg);
  }
  if (msg.type === 'error') {
    ui.showError(msg.code + ': ' + msg.message);
  }
  if (msg.type === 'stats') {
    ui.updateStats(msg);
  }
  if (msg.type === 'event') {
    ui.onEvent(msg);
  }
};

wsClient.onStatusChange = (status) => {
  window.dispatchEvent(new CustomEvent('lazydesk:conn', { detail: status }));
  switch (status) {
    case 'connecting':
      ui.setStatus('连接中...');
      break;
    case 'connected':
      ui.setStatus('已连接', 'connected');
      break;
    case 'disconnected':
      ui.setStatus('已断开', 'disconnected');
      ui.showOverlay('连接断开, 正在重连...');
      break;
  }
};

// ====== WebRTC 状态 → UI ======
webrtc.onState = (state, detail) => {
  if (state === 'video') {
    ui.setStatus('已连接 (视频流)', 'connected');
    ui.hideOverlay();
    ui.clearWatchdog();
  } else if (state === 'pc') {
    switch (detail) {
      case 'connected':
        ui.setStatus('已连接', 'connected');
        ui.clearWatchdog();
        break;
      case 'failed':
        ui.showError('WebRTC 连接失败。请检查:\n1. PC 防火墙放行 UDP\n2. 平板与 PC 同子网\n3. 无 VPN');
        break;
      case 'disconnected':
        ui.setStatus('连接中断...', '');
        break;
    }
  } else if (state === 'fatal') {
    ui.showError(detail);
  }
};

// ====== 连接入口 ======
window.connect = function () {
  const host = (overlayIP.value || '').trim() || defaultHost;
  if (!host) return;
  overlayIP.value = host;
  localStorage.setItem('lazyDesk_host', host);
  webrtc.stop();
  ui.clearWatchdog();
  ui.showOverlay('正在连接...');
  wsClient.connect(host);
};

document.getElementById('connectBtn').addEventListener('click', () => window.connect());

// 自动连接 (记住的地址优先)
const saved = localStorage.getItem('lazyDesk_host');
if (saved) {
  overlayIP.value = saved;
  if (overlayHost) overlayHost.textContent = saved;
}
setTimeout(() => window.connect(), 200);
