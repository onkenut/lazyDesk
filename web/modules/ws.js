// ws.js — WebSocket 通信封装 (ESM)
export class WSClient {
  constructor() {
    this.ws = null;
    this.url = '';
    this.onMessage = null;
    this.onStatusChange = null;
    this.onReady = null;
    this.reconnectTimer = null;
    this.reconnectAttempts = 0;
    this.maxReconnectDelay = 10000;
  }

  connect(host) {
    this.disconnect();
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    this.url = `${protocol}//${host}/ws`;
    this._doConnect();
  }

  _doConnect() {
    console.log(`WebSocket connecting to ${this.url}...`);
    this._setStatus('connecting');

    let ws;
    try {
      ws = new WebSocket(this.url);
    } catch (e) {
      console.error('WebSocket creation failed:', e);
      this._setStatus('disconnected');
      this._scheduleReconnect();
      return;
    }
    this.ws = ws;

    ws.onopen = () => {
      console.log('WebSocket connected');
      this._setStatus('connected');
      this.reconnectAttempts = 0;
    };

    ws.onmessage = (event) => {
      let msg;
      try {
        msg = JSON.parse(event.data);
      } catch (e) {
        console.error('Invalid WS message:', e);
        return;
      }
      if (msg.type === 'server_ready' && this.onReady) this.onReady();
      if (this.onMessage) this.onMessage(msg);
    };

    ws.onclose = (event) => {
      console.log(`WebSocket closed (code: ${event.code})`);
      this.ws = null;
      this._setStatus('disconnected');
      if (event.code !== 1000) this._scheduleReconnect();
    };

    ws.onerror = () => {
      console.error('WebSocket error');
      this._setStatus('disconnected');
    };
  }

  _scheduleReconnect() {
    if (this.reconnectTimer) return;
    const delay = Math.min(1000 * Math.pow(2, this.reconnectAttempts), this.maxReconnectDelay);
    this.reconnectAttempts++;
    console.log(`Reconnecting in ${delay}ms (attempt ${this.reconnectAttempts})...`);
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this._doConnect();
    }, delay);
  }

  send(data) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(data));
    } else {
      console.warn('WebSocket not connected, cannot send:', data.type);
    }
  }

  disconnect() {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.ws) {
      this.ws.close(1000, 'User disconnect');
      this.ws = null;
    }
  }

  _setStatus(status) {
    if (this.onStatusChange) this.onStatusChange(status);
  }

  isConnected() {
    return this.ws && this.ws.readyState === WebSocket.OPEN;
  }
}

export const wsClient = new WSClient();
