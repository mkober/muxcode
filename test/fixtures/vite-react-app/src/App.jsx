import { useState, useEffect } from 'react'

// Test fixture app for Playwright browser monitoring integration tests.
//
// Behavior is controlled by the URL search parameter ?mode=<mode>:
//   ?mode=clean    — no errors or warnings (default)
//   ?mode=error    — emits console.error on mount
//   ?mode=warning  — emits console.warn on mount
//   ?mode=exception — throws an uncaught exception on mount
//   ?mode=all      — emits all three: error, warning, and exception

function App() {
  const [status, setStatus] = useState('loading')

  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const mode = params.get('mode') || 'clean'

    if (mode === 'error' || mode === 'all') {
      console.error('Test error: something went wrong in component initialization')
    }

    if (mode === 'warning' || mode === 'all') {
      console.warn('Test warning: deprecated API usage detected')
    }

    if (mode === 'exception' || mode === 'all') {
      // Defer to next tick so React can finish rendering first
      setTimeout(() => {
        throw new Error('Test exception: uncaught runtime error')
      }, 100)
    }

    setStatus(mode === 'clean' ? 'healthy' : `mode: ${mode}`)
  }, [])

  return (
    <div style={{ padding: '2rem', fontFamily: 'monospace' }}>
      <h1>MuxCode Test App</h1>
      <p>Status: {status}</p>
      <p>This app is a test fixture for Playwright browser monitoring.</p>
    </div>
  )
}

export default App
