#!/usr/bin/env python3
"""lazyDesk v2 前端控制链路验证: 点击虚拟按键 + 文本输入 + 双客户端"""
import asyncio, json, urllib.request, urllib.parse, websockets

CDP = "http://127.0.0.1:9223"
URL = "http://localhost:8080/"

async def run():
    req = urllib.request.Request(CDP + "/json/new?" + urllib.parse.quote(URL), method="PUT")
    with urllib.request.urlopen(req) as r:
        tab = json.load(r)

    async with websockets.connect(tab["webSocketDebuggerUrl"], max_size=10_000_000) as ws:
        mid = 0
        pending = {}
        events = []

        async def cmd(method, params=None):
            nonlocal mid
            mid += 1
            fut = asyncio.get_event_loop().create_future()
            pending[mid] = fut
            await ws.send(json.dumps({"id": mid, "method": method, "params": params or {}}))
            return await asyncio.wait_for(fut, timeout=15)

        async def reader():
            while True:
                try:
                    msg = json.loads(await asyncio.wait_for(ws.recv(), timeout=30))
                except Exception:
                    return
                if "id" in msg and msg["id"] in pending:
                    pending.pop(msg["id"]).set_result(msg)
                    continue
                if "method" not in msg:
                    continue
                if msg["method"] == "Runtime.exceptionThrown":
                    d = msg["params"]["exceptionDetails"]
                    events.append("EXCEPTION: " + json.dumps({"text": d.get("text"), "desc": (d.get("exception") or {}).get("description")}, ensure_ascii=False)[:400])
                elif msg["method"] == "Runtime.consoleAPICalled" and msg["params"]["type"] in ("error",):
                    args = [a.get("value") or a.get("description") for a in msg["params"]["args"]]
                    events.append("CONSOLE[error]: " + " ".join(str(a) for a in args)[:400])

        reader_task = asyncio.ensure_future(reader())
        await cmd("Runtime.enable")
        await asyncio.sleep(8)  # 等待连接

        # 点击 Esc
        await cmd("Runtime.evaluate", {"expression": "document.querySelector('[data-sendkey=escape]').click()", "returnByValue": True})
        await asyncio.sleep(0.5)
        # 点击 Tab
        await cmd("Runtime.evaluate", {"expression": "document.querySelector('[data-sendkey=tab]').click()", "returnByValue": True})
        await asyncio.sleep(0.5)
        # 修饰键 Ctrl 切换
        await cmd("Runtime.evaluate", {"expression": "document.querySelector('.mod-key[data-key=ctrl]').dispatchEvent(new PointerEvent('pointerdown', {bubbles: true}))", "returnByValue": True})
        await asyncio.sleep(0.5)
        # 文本输入
        await cmd("Runtime.evaluate", {"expression": "const i=document.getElementById('textInput'); i.value='v2 测试文本'; document.getElementById('textSendBtn').click()", "returnByValue": True})
        await asyncio.sleep(0.5)
        # 组合键
        await cmd("Runtime.evaluate", {"expression": "document.querySelector('[data-combo=\"ctrl,c\"]').click()", "returnByValue": True})
        await asyncio.sleep(1)

        r = await cmd("Runtime.evaluate", {"expression": "({stats: document.getElementById('statsText').textContent, status: document.getElementById('statusText').textContent, inputCleared: document.getElementById('textInput').value === ''})", "returnByValue": True})
        reader_task.cancel()
        print("PAGE:", json.dumps(r.get("result", {}).get("result", {}).get("value"), ensure_ascii=False))
        print("JS errors:", events if events else "(none)")

asyncio.run(run())
