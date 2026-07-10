// touch.js — 触控→鼠标映射 (基于 canvas 渲染区域)
(function() {
  var layer = document.getElementById('touchLayer');
  var canvas = document.getElementById('remoteCanvas');
  if (!layer) return;

  var sx=0, sy=0, st=0, dragging=false, isTwoFinger=false, longTimer=null;
  var lastMove=0, MOVE_THROTTLE=16; // I1: touchmove 节流 ~60fps
  var lastScroll=0, SCROLL_THROTTLE=50; // 双指滚动节流

  function getLongPressMs() {
    return (window.lazyDeskConfig && window.lazyDeskConfig.longPressMs) || 500;
  }

  // ====== 禁止浏览器手势 ======
  document.addEventListener('contextmenu', function(e){ e.preventDefault(); });
  ['gesturestart','gesturechange','gestureend'].forEach(function(ev){
    document.addEventListener(ev, function(e){ e.preventDefault(); });
  });
  var lastEnd=0;
  document.addEventListener('touchend', function(e){
    if(Date.now()-lastEnd<=300) e.preventDefault();
    lastEnd=Date.now();
  }, { passive:false });
  document.addEventListener('keydown', function(e){
    if(e.ctrlKey && ['+','-','=','0'].indexOf(e.key)>=0) e.preventDefault();
  });

  // ====== 坐标映射: canvas 渲染矩形 → 0..1 比例 ======
  function videoRatio(cx, cy) {
    var r = (canvas && canvas._renderRect) ? canvas._renderRect
      : { left:0, top:0, width:1, height:1 };
    var rect = layer.getBoundingClientRect();
    var rx = (cx - rect.left) / rect.width;
    var ry = (cy - rect.top)  / rect.height;
    // 映射到渲染区域内的比例
    return {
      x: Math.max(0, Math.min(1, (rx - r.left) / r.width)),
      y: Math.max(0, Math.min(1, (ry - r.top)  / r.height))
    };
  }

  // ====== 触控事件 ======
  function onStart(e) {
    e.preventDefault();
    var t=e.touches[0], r=videoRatio(t.clientX, t.clientY);
    sx=r.x; sy=r.y; st=Date.now(); dragging=false;
    if(e.touches.length===1){
      isTwoFinger=false;
      wsClient.send({type:'mouse_move',x:r.x,y:r.y});
      longTimer=setTimeout(function(){
        wsClient.send({type:'mouse_click',button:'right',action:'click'});
      },getLongPressMs());
    }else{ isTwoFinger=true; clearTimeout(longTimer); }
  }
  function onMove(e) {
    e.preventDefault();
    // I1: 节流 — 最多每 16ms 处理一次
    var now = Date.now();
    if (now - lastMove < MOVE_THROTTLE) return;
    lastMove = now;

    if(e.touches.length===1){
      var r=videoRatio(e.touches[0].clientX, e.touches[0].clientY);
      if(!dragging && (Math.abs(r.x-sx)>0.005||Math.abs(r.y-sy)>0.005)){
        dragging=true; clearTimeout(longTimer);
        wsClient.send({type:'mouse_click',button:'left',action:'down'});
      }
      if(dragging) wsClient.send({type:'mouse_move',x:r.x,y:r.y});
    }else if(e.touches.length===2){
      clearTimeout(longTimer);
      var r1=videoRatio(e.touches[0].clientX, e.touches[0].clientY);
      var r2=videoRatio(e.touches[1].clientX, e.touches[1].clientY);
      var cy=(r1.y+r2.y)/2;
      // I5: 增大阈值防方向抖动
      if(Math.abs(cy-sy)>0.015){ wsClient.send({type:'mouse_scroll',deltaY:cy>sy?10:-10}); sy=cy; }
    }
  }
  function onEnd(e) {
    e.preventDefault(); clearTimeout(longTimer);
    // 双指操作结束后不触发点击
    if(!dragging && !isTwoFinger && e.changedTouches.length===1 && Date.now()-st<getLongPressMs()){
      wsClient.send({type:'mouse_click',button:'left',action:'click'});
    }
    if(dragging && e.touches.length===0){
      wsClient.send({type:'mouse_click',button:'left',action:'up'});
    }
    if(e.touches.length===0){ isTwoFinger=false; dragging=false; }
  }

  layer.addEventListener('touchstart', onStart, {passive:false});
  layer.addEventListener('touchmove',  onMove,  {passive:false});
  layer.addEventListener('touchend',   onEnd,   {passive:false});

  // 桌面调试: 鼠标点击
  layer.addEventListener('mousedown', function(e){
    var r=videoRatio(e.clientX, e.clientY);
    wsClient.send({type:'mouse_move',x:r.x,y:r.y});
    wsClient.send({type:'mouse_click',button:'left',action:'click'});
  });
})();
