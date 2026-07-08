// webrtc.js — WebRTC 连接管理
var pc = null;
var videoReady = false;
var remoteDescSet = false;   // remote description 是否已设置
var iceBuffer = [];           // 缓冲的 ICE candidates

function startWebRTC() {
  const remoteVideo = document.getElementById('remoteVideo');
  if (!remoteVideo) {
    console.error('remoteVideo element not found');
    return;
  }

  console.log('Starting WebRTC...');
  remoteDescSet = false;
  iceBuffer = [];

  const config = { iceServers: [] };
  pc = new RTCPeerConnection(config);

  // 接收远程 track (视频/音频)
  pc.ontrack = (event) => {
    console.log('ontrack:', event.track.kind, 'streams:', event.streams.length);
    if (event.streams.length > 0) {
      const stream = event.streams[0];
      if (remoteVideo.srcObject !== stream) {
        remoteVideo.srcObject = stream;
        console.log('Video srcObject set');
      }
      remoteVideo.play().then(() => {
        console.log('Video playing ✓');
        videoReady = true;
        if (document.getElementById('statusText')) {
          document.getElementById('statusText').textContent = '已连接 (视频流)';
        }
      }).catch(e => {
        console.error('Video autoplay blocked:', e.name);
        // 显示点击播放提示
        if (document.getElementById('statusText')) {
          document.getElementById('statusText').textContent = '已连接 (点画面播放)';
        }
        // 全页面点击事件解除静音
        document.addEventListener('click', () => {
          remoteVideo.play().then(() => {
            console.log('Video playing after click');
            videoReady = true;
            if (document.getElementById('statusText')) {
              document.getElementById('statusText').textContent = '已连接 (视频流)';
            }
          }).catch(() => {});
        }, { once: true });
      });
    }
  };

  // ICE candidate → 发送给服务器
  pc.onicecandidate = (event) => {
    if (event.candidate) {
      console.log('ICE candidate (client):', event.candidate.type);
      wsClient.send({
        type: 'candidate',
        candidate: event.candidate.candidate,
        sdpMid: event.candidate.sdpMid,
        sdpMLineIndex: event.candidate.sdpMLineIndex
      });
    } else {
      console.log('ICE gathering complete (client)');
    }
  };

  // 连接状态
  pc.onconnectionstatechange = () => {
    console.log('WebRTC state:', pc.connectionState);
    const dot = document.getElementById('statusDot');
    const text = document.getElementById('statusText');
    
    switch (pc.connectionState) {
      case 'connected':
        if (dot) dot.className = 'connected';
        if (text) text.textContent = videoReady ? '已连接 (视频流)' : '已连接';
        break;
      case 'connecting':
        if (dot) dot.className = '';
        if (text) text.textContent = 'WebRTC 连接中...';
        break;
      case 'failed':
      case 'disconnected':
        if (dot) dot.className = 'disconnected';
        if (text) text.textContent = 'WebRTC 断开';
        break;
    }
  };

  // ICE 连接状态 (更细粒度)
  pc.oniceconnectionstatechange = () => {
    console.log('ICE state:', pc.iceConnectionState);
  };

  // 信令状态
  pc.onsignalingstatechange = () => {
    console.log('Signaling state:', pc.signalingState);
  };

  // 创建 Offer
  pc.createOffer({
    offerToReceiveAudio: false,
    offerToReceiveVideo: true
  }).then(offer => {
    console.log('Offer created, setting local description...');
    return pc.setLocalDescription(offer);
  }).then(() => {
    console.log('Local description set, sending offer to server...');
    wsClient.send({
      type: 'offer',
      sdp: pc.localDescription.sdp
    });
  }).catch(err => {
    console.error('Failed to create offer:', err);
  });
}

// 处理来自服务器的信令消息
function handleWebRTCSignal(msg) {
  if (!pc) {
    console.warn('handleWebRTCSignal: pc is null');
    return;
  }

  switch (msg.type) {
    case 'answer':
      console.log('Setting remote description (answer)...');
      pc.setRemoteDescription(new RTCSessionDescription({
        type: 'answer',
        sdp: msg.sdp
      })).then(() => {
        console.log('Remote description set ✓');
        remoteDescSet = true;
        
        // 处理缓冲的 ICE candidates
        if (iceBuffer.length > 0) {
          console.log('Processing', iceBuffer.length, 'buffered ICE candidates');
          iceBuffer.forEach(c => {
            pc.addIceCandidate(c).catch(err => {
              console.error('Buffered ICE add error:', err);
            });
          });
          iceBuffer = [];
        }
      }).catch(err => {
        console.error('Set remote description error:', err);
      });
      break;

    case 'candidate':
      if (msg.candidate) {
        const candidate = new RTCIceCandidate({
          candidate: msg.candidate,
          sdpMid: msg.sdpMid || null,
          sdpMLineIndex: msg.sdpMLineIndex ?? null
        });
        
        if (remoteDescSet) {
          console.log('Adding ICE candidate (server)');
          pc.addIceCandidate(candidate).catch(err => {
            console.error('Add ICE candidate error:', err);
          });
        } else {
          // 缓冲 ICE candidates 直到 remote desc 设置完成
          console.log('Buffering ICE candidate (remote desc not set yet)');
          iceBuffer.push(candidate);
        }
      }
      break;
  }
}
