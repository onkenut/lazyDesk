// ws.js — WebSocket 通信封装
class WSClient {
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

    try {
      this.ws = new WebSocket(this.url);
    } catch (e) {
      console.error('WebSocket creation failed:', e);
      this._setStatus('disconnected');
      this._scheduleReconnect();
      return;
    }

    this.ws.onopen = () => {
      console.log('WebSocket connected');
      this._setStatus('connected');
      this.reconnectAttempts = 0;
      // 连接成功后，等待 server_ready 消息
    };

    this.ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data);
        console.log('WS ←', msg.type);
        
        // server_ready 触发 WebRTC 初始化
        if (msg.type === 'server_ready') {
          console.log('Server ready, starting WebRTC...');
          if (this.onReady) this.onReady();
        }
        
        // 错误消息
        if (msg.type === 'error') {
          console.error('Server error:', msg.code, msg.message);
        }
        
        // 转发所有消息
        if (this.onMessage) this.onMessage(msg);
      } catch (e) {
        console.error('Failed to parse WS message:', e);
      }
    };

    this.ws.onclose = (event) => {
      console.log(`WebSocket closed (code: ${event.code})`);
      this._setStatus('disconnected');
      this.ws = null;
      if (event.code !== 1000) {
        this._scheduleReconnect();
      }
    };

    this.ws.onerror = (error) => {
      console.error('WebSocket error:', error);
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
      const json = JSON.stringify(data);
      console.log('WS →', data.type);
      this.ws.send(json);
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

const wsClient = new WSClient();
