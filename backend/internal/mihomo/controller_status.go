package mihomo

// ManagedController supplies the controller currently configured by the web
// manager. Credentials remain internal and must never be sent to the client.
func ManagedController() (controller, secret string, found bool) {
	managers.Range(func(key, _ any) bool {
		m, ok := key.(*Manager)
		if !ok {
			return true
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.closed || m.saved.Secret == "" {
			return true
		}
		controller = m.controllerURL
		if controller == "" {
			controller = "http://127.0.0.1:9098"
		}
		secret = m.saved.Secret
		found = true
		return false
	})
	return
}
