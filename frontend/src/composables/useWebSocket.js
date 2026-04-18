import { ref } from 'vue'

export function useWebSocket() {
  const connected = ref(false)
  const error = ref(null)
  let ws = null

  function getWsUrl(name, sessionId) {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${proto}//${location.host}/api/v1/vms/${name}/shell/${sessionId}`
  }

  // connect(vmName, sessionId, onData) — VM serial console path, preserved
  // for the existing ConsoleTerminal callsite.
  // connect(url, onData) — connect to an arbitrary ws path (used by
  // K9sTerminal). Detected by treating a leading "/" or full URL as the
  // first form's URL, since neither is a valid VM name.
  function connect(arg1, arg2, arg3) {
    let url, onData
    if (typeof arg2 === 'function') {
      url = resolveUrl(arg1)
      onData = arg2
    } else {
      url = getWsUrl(arg1, arg2)
      onData = arg3
    }

    disconnect()
    error.value = null

    ws = new WebSocket(url)
    ws.binaryType = 'arraybuffer'

    ws.onopen = () => { connected.value = true }

    ws.onmessage = (event) => {
      if (onData) {
        const data = event.data instanceof ArrayBuffer
          ? new Uint8Array(event.data)
          : new TextEncoder().encode(event.data)
        onData(data)
      }
    }

    ws.onclose = (event) => {
      connected.value = false
      // 1001 = GoingAway: PTY process exited on the server
      if (event.code === 1001) {
        error.value = 'Shell process exited'
      }
    }

    ws.onerror = (e) => {
      error.value = 'Connection failed'
      connected.value = false
    }
  }

  function send(data) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(data)
    }
  }

  function sendResize(cols, rows) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      const buf = new Uint8Array(5)
      buf[0] = 1 // resize prefix
      buf[1] = (cols >> 8) & 0xff
      buf[2] = cols & 0xff
      buf[3] = (rows >> 8) & 0xff
      buf[4] = rows & 0xff
      ws.send(buf)
    }
  }

  function resolveUrl(pathOrUrl) {
    if (/^wss?:/i.test(pathOrUrl)) return pathOrUrl
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const path = pathOrUrl.startsWith('/') ? pathOrUrl : '/' + pathOrUrl
    return `${proto}//${location.host}${path}`
  }

  function disconnect() {
    if (ws) {
      ws.close()
      ws = null
    }
    connected.value = false
  }

  return { connected, error, connect, send, sendResize, disconnect }
}
