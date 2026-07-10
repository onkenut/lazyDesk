// webrtc.js — WebRTC + canvas 渲染
var pc = null;
var videoReady = false;
var remoteDescSet = false;
var iceBuffer = [];
var drawRAF = null;

var RTCPeerConnection = window.RTCPeerConnection
  || window.webkitRTCPeerConnection
  || window.mozRTCPeerConnection;

function startWebRTC() {
  if (!RTCPeerConnection) {
    showFatal('浏览器不支持 WebRTC。\n请使用最新版 Chrome 或 Edge。');
    return;
  }

  // 先清理旧的 PeerConnection (断线重连场景)
  if (window.pc) {
    try { window.pc.close(); } catch(e) {}
    window.pc = null;
  }
  videoReady = false;
  remoteDescSet = false;
  iceBuffer = [];
  if (drawRAF) { cancelAnimationFrame(drawRAF); drawRAF = null; }

  var video = document.getElementById('remoteVideo');
  var canvas = document.getElementById('remoteCanvas');
  if (!video || !canvas) return;

  console.log('Starting WebRTC...');
  remoteDescSet = false;
  iceBuffer = [];

  try { pc = new RTCPeerConnection({ iceServers: [] }); }
  catch(e) { showFatal('创建连接失败: '+e.message); return; }
  window.pc = pc; // 供 app.js watchdog/重连清理访问

  pc.ontrack = function(e) {
    console.log('ontrack:', e.track.kind);
    if (e.streams.length > 0) {
      var stream = e.streams[0];
      video.srcObject = stream;
      video.play().then(function() {
        console.log('video playing, starting canvas render');
        videoReady = true;
        updateStatus('已连接 (视频流)', 'connected');
        startCanvasRender(video, canvas);
      }).catch(function(err) {
        console.warn('autoplay blocked:', err.name);
        updateStatus('已连接 (点画面播放)', '');
        // 用户点击后启动
        document.addEventListener('click', function() {
          video.play().then(function() {
            videoReady = true;
            startCanvasRender(video, canvas);
            updateStatus('已连接 (视频流)', 'connected');
          }).catch(function(){});
        }, { once: true });
      });
    }
  };

  pc.onicecandidate = function(e) {
    if (e.candidate) wsClient.send({
      type: 'candidate', candidate: e.candidate.candidate,
      sdpMid: e.candidate.sdpMid, sdpMLineIndex: e.candidate.sdpMLineIndex
    });
  };

  pc.onconnectionstatechange = function() {
    var s = pc.connectionState;
    console.log('WebRTC:', s);
    switch (s) {
      case 'connected':  videoReady || updateStatus('已连接', 'connected'); break;
      case 'failed':     showFatal('连接失败。请检查:\n1.PC防火墙UDP端口\n2.平板与PC同子网\n3.无VPN'); break;
      case 'disconnected': updateStatus('WebRTC断开, 等待重连...', 'disconnected'); break;
    }
  };
  pc.oniceconnectionstatechange = function() { console.log('ICE:', pc.iceConnectionState); };
  pc.onsignalingstatechange = function() { console.log('Signaling:', pc.signalingState); };

  // 使用 addTransceiver (替代过时的 offerToReceiveVideo)
  pc.addTransceiver('video', { direction: 'recvonly' });
  pc.createOffer()
    .then(function(offer) {
      console.log('Offer, video:', /m=video/.test(offer.sdp));
      return pc.setLocalDescription(offer);
    })
    .then(function() { wsClient.send({ type: 'offer', sdp: pc.localDescription.sdp }); })
    .catch(function(err) { showFatal('Offer失败: '+err.message); });
}

// ====== canvas 渲染循环 ======
var _resizeHandler = null;

function startCanvasRender(video, canvas) {
  if (drawRAF) cancelAnimationFrame(drawRAF);
  var ctx = canvas.getContext('2d');

  function resize() {
    canvas.width  = window.innerWidth  * (window.devicePixelRatio || 1);
    canvas.height = window.innerHeight * (window.devicePixelRatio || 1);
    canvas.style.width  = '100vw';
    canvas.style.height = '100vh';
  }
  resize();

  // 防止断线重连时事件监听堆积
  if (_resizeHandler) window.removeEventListener('resize', _resizeHandler);
  _resizeHandler = resize;
  window.addEventListener('resize', resize);

  function draw() {
    // readyState >= 2 (HAVE_CURRENT_DATA) 才有帧画面可绘
    if (!videoReady || video.readyState < 2) {
      drawRAF = requestAnimationFrame(draw);
      return;
    }

    var vw = video.videoWidth  || 1920;
    var vh = video.videoHeight || 1080;
    var cw = canvas.width;
    var ch = canvas.height;
    var va = vw / vh;
    var ca = cw / ch;
    var dx, dy, dw, dh;

    if (ca > va) { dh = ch; dw = ch * va; dx = (cw - dw) / 2; dy = 0; }
    else         { dw = cw; dh = cw / va; dx = 0; dy = (ch - dh) / 2; }

    ctx.fillStyle = '#000';
    ctx.fillRect(0, 0, cw, ch);
    ctx.drawImage(video, dx, dy, dw, dh);

    // 存储渲染区域 (供 touch.js 读取坐标映射)
    canvas._renderRect = { left: dx/cw, top: dy/ch, width: dw/cw, height: dh/ch };

    drawRAF = requestAnimationFrame(draw);
  }
  draw();
}

function handleWebRTCSignal(msg) {
  if (!pc) return;
  if (msg.type === 'answer') {
    console.log('Answer, video:', /m=video/.test(msg.sdp));
    pc.setRemoteDescription(new RTCSessionDescription({ type:'answer', sdp:msg.sdp }))
      .then(function() {
        remoteDescSet = true;
        if (iceBuffer.length) { iceBuffer.forEach(function(c){ pc.addIceCandidate(c).catch(function(){}); }); iceBuffer=[]; }
      }).catch(function(e){ console.error(e); });
  } else if (msg.type === 'candidate' && msg.candidate) {
    var c = new RTCIceCandidate({ candidate:msg.candidate, sdpMid:msg.sdpMid, sdpMLineIndex:msg.sdpMLineIndex });
    remoteDescSet ? pc.addIceCandidate(c).catch(function(){}) : iceBuffer.push(c);
  }
}

function updateStatus(text, cls) {
  var t=document.getElementById('statusText'), d=document.getElementById('statusDot');
  if(t) t.textContent=text; if(d) d.className=cls;
}
function showFatal(msg) {
  console.error(msg); updateStatus('WebRTC错误','disconnected');
  var o=document.getElementById('connectOverlay'), x=o?o.querySelector('.overlay-text'):null;
  if(o) o.style.display='flex'; if(x) x.textContent=msg;
}
