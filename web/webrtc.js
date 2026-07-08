// webrtc.js — WebRTC 连接管理
let pc = null;
let remoteVideo = null;

function startWebRTC() {
  remoteVideo = document.getElementById('remoteVideo');

  // 创建 PeerConnection (无 ICE 服务器 — 纯局域网)
  const config = {
    iceServers: []  // 局域网不需要 STUN/TURN
  };

  pc = new RTCPeerConnection(config);

  // 监听远程 track (视频/音频)
  pc.ontrack = (event) => {
    console.log('Received remote track:', event.track.kind);
    if (remoteVideo.srcObject !== event.streams[0]) {
      remoteVideo.srcObject = event.streams[0];
    }
  };

  // 监听 ICE 候选
  pc.onicecandidate = (event) => {
    if (event.candidate) {
      wsClient.send({
        type: 'candidate',
        candidate: event.candidate.candidate,
        sdpMid: event.candidate.sdpMid,
        sdpMLineIndex: event.candidate.sdpMLineIndex
      });
    }
  };

  // 监听连接状态
  pc.onconnectionstatechange = () => {
    console.log('WebRTC state:', pc.connectionState);
    if (pc.connectionState === 'connected') {
      // 解除静音 (初始 muted 是为了绕过 autoplay 策略)
      remoteVideo.muted = false;
      remoteVideo.play().catch(e => console.log('Play error (may need user gesture):', e));
    }
  };

  // 创建 Offer
  pc.createOffer({
    offerToReceiveAudio: true,
    offerToReceiveVideo: true
  }).then(offer => {
    return pc.setLocalDescription(offer);
  }).then(() => {
    // 发送 Offer SDP 给服务器
    wsClient.send({
      type: 'offer',
      sdp: pc.localDescription.sdp
    });
  }).catch(err => {
    console.error('Failed to create offer:', err);
  });
}

// 处理来自 WebSocket 的 WebRTC 信令消息
function handleWebRTCSignal(msg) {
  if (!pc) return;

  switch (msg.type) {
    case 'answer':
      pc.setRemoteDescription(new RTCSessionDescription({
        type: 'answer',
        sdp: msg.sdp
      })).catch(err => console.error('Set remote answer error:', err));
      break;

    case 'candidate':
      if (msg.candidate) {
        pc.addIceCandidate(new RTCIceCandidate({
          candidate: msg.candidate,
          sdpMid: msg.sdpMid || null,
          sdpMLineIndex: msg.sdpMLineIndex ?? null
        })).catch(err => console.error('Add ICE candidate error:', err));
      }
      break;
  }
}
