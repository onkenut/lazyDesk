#!/usr/bin/env python3
"""lazyDesk v2 前端诊断: 抓取浏览器 console 错误 + 异常 + WebRTC 状态"""
import asyncio, json, urllib.request, urllib.parse, websockets

CDP = "http://127.0.0.1:9223"
URL = "http://localhost:8080/"

async def run():
    req = urllib.request.Request(CDP + "/json/new?" + urllib.parse.quote(URL), method="PUT")
    with urllib.request.urlopen(req) as r:
        tab = json.load(r)

    async with websockets.connect(tab["webSocketDebuggerUrl"], max_size=10_000_000) as ws:
        mid = 0
        pending = {}   # id -> future
        events = []

        async def cmd(method, params=None):
            nonlocal mid
            mid += 1
            fut = asyncio.get_event_loop().create_future()
            pending[mid] = fut
            await ws.send(json.dumps({"id": mid, "method": method, "params": params or {}}))
            return await asyncio.wait_for(fut, timeout=15)

        # 单一 recv 循环: 分发响应 + 收集事件
        async def reader():
            while True:
                try:
                    msg = json.loads(await asyncio.wait_for(ws.recv(), timeout=30))
                except asyncio.TimeoutError:
                    continue
                except Exception:
                    return
                if "id" in msg and msg["id"] in pending:
                    pending.pop(msg["id"]).set_result(msg)
                    continue
                if "method" not in msg:
                    continue
                m = msg["method"]
                if m == "Runtime.exceptionThrown":
                    d = msg["params"]["exceptionDetails"]
                    events.append("EXCEPTION: " + json.dumps({
                        "text": d.get("text"), "desc": (d.get("exception") or {}).get("description")
                    }, ensure_ascii=False)[:600])
                elif m == "Runtime.consoleAPICalled":
                    t = msg["params"]["type"]
                    if t in ("error", "warning"):
                        args = [a.get("value") or a.get("description") for a in msg["params"]["args"]]
                        events.append(f"CONSOLE[{t}]: " + " ".join(str(a) for a in args)[:600])
                elif m == "Log.entryAdded":
                    e = msg["params"]["entry"]
                    events.append(f"LOG[{e.get('level')}]: {e.get('text')}"[:600])

        reader_task = asyncio.ensure_future(reader())
        await cmd("Runtime.enable")
        await cmd("Log.enable")
        await asyncio.sleep(13)

        # 读取页面状态 + WebRTC 统计
        JS = """(async () => {
          const q = (id) => document.getElementById(id);
          let rtc = null;
          try {
            const pc = window.__pc;
            if (pc) {
              const stats = await pc.getStats();
              const inbound = [];
              stats.forEach((s) => {
                if (s.type === 'inbound-rtp' && s.kind) {
                  inbound.push({
                    kind: s.kind,
                    packetsReceived: s.packetsReceived,
                    bytesReceived: s.bytesReceived,
                    framesDecoded: s.framesDecoded,
                    keyFramesDecoded: s.keyFramesDecoded,
                    framesReceived: s.framesReceived,
                    frameWidth: s.frameWidth,
                    frameHeight: s.frameHeight,
                    jitter: s.jitter,
                    packetsLost: s.packetsLost,
                    nackCount: s.nackCount,
                    pliCount: s.pliCount,
                    firCount: s.firCount,
                    discardedPackets: s.discardedPackets,
                    codecId: s.codecId,
                  });
                }
              });
              rtc = {
                connState: pc.connectionState,
                iceState: pc.iceConnectionState,
                inbound,
              };
            }
          } catch (e) { rtc = { error: e.message }; }
          return {
            status: q('statusText')?.textContent,
            stats: q('statsText')?.textContent,
            overlay: q('connectOverlay')?.style.display,
            videoRS: q('remoteVideo')?.readyState,
            audioRS: q('remoteAudio')?.readyState,
            videoSrc: q('remoteVideo')?.srcObject ? 'set' : 'null',
            rtc,
          };
        })()"""
        r = await cmd("Runtime.evaluate", {"expression": JS, "returnByValue": True, "awaitPromise": True})
        reader_task.cancel()
        print("PAGE:", json.dumps(r.get("result", {}).get("result", {}).get("value"), ensure_ascii=False))
        print("--- console/log events ---")
        for e in events:
            print(e)
        if not events:
            print("(no console errors)")

asyncio.run(run())
