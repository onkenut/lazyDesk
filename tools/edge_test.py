#!/usr/bin/env python3
"""lazyDesk v2 前端端到端验证 (Edge headless + CDP)
验证: 页面加载 → WS 连接 → WebRTC 协商 → 视频渲染 (canvas 像素) → 音频
"""
import asyncio, json, urllib.request, urllib.parse, websockets

CDP = "http://127.0.0.1:9223"
URL = "http://localhost:8080/"

JS = """
(() => {
  const q = (id) => document.getElementById(id);
  const canvas = q('remoteCanvas');
  const video = q('remoteVideo');
  let px = { sampled: 0, nonBlack: 0, black: 0 };
  try {
    const ctx = canvas.getContext('2d');
    const w = canvas.width, h = canvas.height;
    if (w > 0 && h > 0) {
      const img = ctx.getImageData(0, 0, Math.min(w, 400), Math.min(h, 400));
      const d = img.data;
      for (let i = 0; i < d.length; i += 4) {
        px.sampled++;
        if (d[i] > 20 || d[i+1] > 20 || d[i+2] > 20) px.nonBlack++;
        else px.black++;
      }
    }
  } catch (e) { px.error = e.message; }
  return {
    statusText: q('statusText')?.textContent,
    statsText: q('statsText')?.textContent,
    overlayDisplay: q('connectOverlay')?.style.display,
    videoReadyState: video?.readyState,
    videoW: video?.videoWidth, videoH: video?.videoHeight,
    audioReadyState: q('remoteAudio')?.readyState,
    canvasW: canvas.width, canvasH: canvas.height,
    px,
  };
})()
"""

async def run():
    req = urllib.request.Request(CDP + "/json/new?" + urllib.parse.quote(URL), method="PUT")
    with urllib.request.urlopen(req) as r:
        tab = json.load(r)
    print(f"[CDP] tab: {tab['id']}")

    async with websockets.connect(tab["webSocketDebuggerUrl"], max_size=10_000_000) as ws:
        mid = 0
        async def cmd(method, params=None):
            nonlocal mid
            mid += 1
            await ws.send(json.dumps({"id": mid, "method": method, "params": params or {}}))
            while True:
                resp = json.loads(await ws.recv())
                if resp.get("id") == mid:
                    return resp

        await cmd("Runtime.enable")
        await asyncio.sleep(12)  # 等待 WS + WebRTC + 首帧

        r = await cmd("Runtime.evaluate", {"expression": JS, "returnByValue": True})
        print(json.dumps(r.get("result", {}).get("result", {}).get("value"), ensure_ascii=False, indent=2))

asyncio.run(run())
