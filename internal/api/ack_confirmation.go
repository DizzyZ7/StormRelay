package api

import (
	"html/template"
	"net/http"
	"net/url"
)

// Viewing an acknowledgement URL must never change incident state: chat link
// previews, crawlers and security scanners routinely issue GET and HEAD requests.
var acknowledgementConfirmation = template.Must(template.New("ack-confirmation").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Confirm incident acknowledgement · StormRelay</title>
</head>
<body>
<main>
<h1>Acknowledge this incident?</h1>
<p>Opening this page has not acknowledged anything. Continue only if you intend to take responsibility for this incident.</p>
<form method="post" action="{{.Action}}">
<button type="submit">Acknowledge incident</button>
</form>
</main>
</body>
</html>`))

func (s *Server) ackConfirmation(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if len(token) < 20 {
		writeError(w, r, http.StatusNotFound, "not_found", "acknowledgement token is invalid", nil)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	if err := acknowledgementConfirmation.Execute(w, struct{ Action string }{
		Action: "/api/v1/ack/" + url.PathEscape(token),
	}); err != nil {
		s.logger.Error("acknowledgement confirmation render failed", "error", err)
	}
}
