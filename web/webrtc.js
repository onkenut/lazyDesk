// webrtc.js — WebRTC 连接管理
var pc = null;
var videoReady = false;
var remoteDescSet = false;
var iceBuffer = [];

function startWebRTC() {
  const video = document.getElementById('remoteVideo');
  if (!video) { console.error('video element missing'); return; }

  console.log('Starting WebRTC...');
  remoteDescSet = false;
  iceBuffer = [];

  pc = new RTCPeerConnection({ iceServers: [] });

  // 接收远程 track
  pc.ontrack = (event) => {
    console.log('ontrack:', event.track.kind, 'streams:', event.streams.length);
    if (event.streams.length > 0) {
      const stream = event.streams[0];
      video.srcObject = stream;
      console.log('Video srcObject set, calling play()...');
      video.play().then(() => {
        console.log('Video playing ✓');
        videoReady = true;
        updateStatus('已连接 (视频流)', 'connected');
      }).catch(e => {
        console.warn('Autoplay blocked:', e.name, '— waiting for user click');
        updateStatus('已连接 (点画面播放)', '');
      });
    }
  };

  // 收集 ICE candidates → 发给服务器
  pc.onicecandidate = (e) => {
    if (e.candidate) {
      console.log('ICE (client):', e.candidate.type, e.candidate.protocol);
      wsClient.send({
        type: 'candidate', candidate: e.candidate.candidate,
        sdpMid: e.candidate.sdpMid, sdpMLineIndex: e.candidate.sdpMLineIndex
      });
    } else console.log('ICE gathering done (client)');
  };

  // 连接状态 → UI 更新
  pc.onconnectionstatechange = () => {
    const s = pc.connectionState;
    console.log('WebRTC state:', s);
    switch (s) {
      case 'connected':  videoReady || updateStatus('已连接', 'connected'); break;
      case 'connecting': updateStatus('WebRTC 连接中...', ''); break;
      case 'failed':     updateStatus('WebRTC 失败 — 检查防火墙', 'disconnected'); break;
      case 'disconnected': updateStatus('WebRTC 断开', 'disconnected'); break;
    }
  };
  pc.oniceconnectionstatechange = () => console.log('ICE state:', pc.iceConnectionState);
  pc.onsignalingstatechange    = () => console.log('Signaling:', pc.signalingState);

  // 收集 ICE gathering 状态
  pc.onicegatheringstatechange = () => console.log('ICE gathering:', pc.iceGatheringState);

  // 创建 Offer
  pc.createOffer({ offerToReceiveAudio: false, offerToReceiveVideo: true })
    .then(offer => {
      console.log('Offer SDP has video:', /m=video/.test(offer.sdp));
      return pc.setLocalDescription(offer);
    })
    .then(() => {
      console.log('Local desc set, sending offer');
      wsClient.send({ type: 'offer', sdp: pc.localDescription.sdp });
    })
    .catch(err => console.error('createOffer failed:', err));
}

// 处理服务器信令
function handleWebRTCSignal(msg) {
  if (!pc) { console.warn('pc null, ignoring signal'); return; }

  if (msg.type === 'answer') {
    console.log('Answer SDP has video:', /m=video/.test(msg.sdp));
    pc.setRemoteDescription(new RTCSessionDescription({
      type: 'answer', sdp: msg.sdp
    })).then(() => {
      console.log('Remote desc set ✓');
      remoteDescSet = true;
      // 处理缓冲的 ICE
      if (iceBuffer.length) {
        console.log('Flushing', iceBuffer.length, 'buffered ICE');
        iceBuffer.forEach(c => pc.addIceCandidate(c).catch(() => {}));
        iceBuffer = [];
      }
    }).catch(err => console.error('setRemoteDescription:', err));

  } else if (msg.type === 'candidate' && msg.candidate) {
    const c = new RTCIceCandidate({
      candidate: msg.candidate, sdpMid: msg.sdpMid,
      sdpMLineIndex: msg.sdpMLineIndex
    });
    if (remoteDescSet) {
      pc.addIceCandidate(c).catch(err => console.error('addIceCandidate:', err));
    } else {
      console.log('Buffering ICE (remote desc not set)');
      iceBuffer.push(c);
    }
  }
}

function updateStatus(text, dotClass) {
  const t = document.getElementById('statusText');
  const d = document.getElementById('statusDot');
  if (t) t.textContent = text;
  if (d) d.className = dotClass;
}
