package http

import nethttp "net/http"

// cliPingApp 是 ping 返回的应用标识。CLI 用它确认端口上确实是本应用，而不是端口被别的程序复用。
const cliPingApp = "miaoju-desktop"

func cliPing(version string) nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		writeJSON(w, nethttp.StatusOK, map[string]string{"app": cliPingApp, "version": version})
	}
}
