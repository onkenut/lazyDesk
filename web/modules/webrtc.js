// webrtc.js — 每客户端 WebRTC 连接管理 (视频 + 音频)
import { wsClient } from './ws.js';

const RTCPeerConnectionCtor =
  window.RTCPeerConnection || window.webkitRTCPeerConnection || window.mozRTCPeerConnection;

export const webrtc = {
  pc: null,
  videoReady: false,
  renderer: null,
  iceBuffer: [],
  remoteDescSet: false,
  onState: null, // (state: string, detail?: string) => void
  onVideoReady: null,

  start(renderer) {
    if (!RTCPeerConnectionCtor) {
      if (this.onState) this.onState('fatal', '浏览器不支持 WebRTC, 请使用新版 Chrome/Edge');
      return;
    }
    this.stop();

    const video = document.getElementById('remoteVideo');
    const audio = document.getElementById('remoteAudio');
    if (!video || !audio) return;
    this.renderer = renderer;
    this.videoReady = false;
    this.remoteDescSet = false;
    this.iceBuffer = [];

    let pc;
    try {
      pc = new RTCPeerConnectionCtor({ iceServers: [] });
    } catch (e) {
      if (this.onState) this.onState('fatal', '创建连接失败: ' + e.message);
      return;
    }
    this.pc = pc;
    // 调试钩子: 供诊断工具读取 getStats (不影响功能)
    window.__pc = pc;

    pc.ontrack = (e) => {
      if (e.track.kind === 'audio') {
        if (e.streams.length > 0) audio.srcObject = e.streams[0];
        audio.play().catch(() => {});
        return;
      }
      if (e.streams.length > 0) video.srcObject = e.streams[0];
      video.play().then(() => {
        this.videoReady = true;
        renderer.start();
        if (this.onState) this.onState('video', '');
        if (this.onVideoReady) this.onVideoReady();
      }).catch(() => {
        // 自动播放被拦截: 等首次交互
        document.addEventListener('click', () => {
          video.play().then(() => {
            this.videoReady = true;
            renderer.start();
            if (this.onVideoReady) this.onVideoReady();
          }).catch(() => {});
        }, { once: true });
      });
    };

    pc.onicecandidate = (e) => {
      if (e.candidate) {
        wsClient.send({
          type: 'candidate',
          candidate: e.candidate.candidate,
          sdpMid: e.candidate.sdpMid,
          sdpMLineIndex: e.candidate.sdpMLineIndex,
        });
      }
    };

    pc.onconnectionstatechange = () => {
      const s = pc.connectionState;
      if (this.onState) this.onState('pc', s);
    };

    // 视频 + 音频接收
    pc.addTransceiver('video', { direction: 'recvonly' });
    pc.addTransceiver('audio', { direction: 'recvonly' });

    pc.createOffer()
      .then((offer) => pc.setLocalDescription(offer))
      .then(() => wsClient.send({ type: 'offer', sdp: pc.localDescription.sdp }))
      .catch((err) => {
        if (this.onState) this.onState('fatal', '创建 Offer 失败: ' + err.message);
      });
  },

  handleSignal(msg) {
    const pc = this.pc;
    if (!pc) return;
    if (msg.type === 'answer') {
      pc.setRemoteDescription(new RTCSessionDescription({ type: 'answer', sdp: msg.sdp }))
        .then(() => {
          this.remoteDescSet = true;
          const pending = this.iceBuffer;
          this.iceBuffer = [];
          pending.forEach((c) => pc.addIceCandidate(c).catch(() => {}));
        })
        .catch((e) => console.error('setRemoteDescription failed:', e));
    } else if (msg.type === 'candidate' && msg.candidate) {
      const c = new RTCIceCandidate({
        candidate: msg.candidate,
        sdpMid: msg.sdpMid,
        sdpMLineIndex: msg.sdpMLineIndex,
      });
      if (this.remoteDescSet) pc.addIceCandidate(c).catch(() => {});
      else this.iceBuffer.push(c);
    }
  },

  stop() {
    this.iceBuffer = [];
    this.remoteDescSet = false;
    this.videoReady = false;
    if (this.pc) {
      try { this.pc.close(); } catch (e) {}
      this.pc = null;
    }
    if (this.renderer) this.renderer.stop();
    this.renderer = null;
  },
};
