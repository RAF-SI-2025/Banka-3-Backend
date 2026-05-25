import json
import os
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib import request


def build_message(payload):
    alerts = payload.get("alerts", [])
    status = payload.get("status", "firing").upper()
    if not alerts:
        return {"content": f"[{status}] Alertmanager sent an empty payload."}

    lines = [f"[{status}] {len(alerts)} alert(s)"]
    for alert in alerts:
        labels = alert.get("labels", {})
        annotations = alert.get("annotations", {})
        summary = annotations.get("summary") or labels.get("alertname", "Alert")
        description = annotations.get("description", "")
        severity = labels.get("severity", "info")
        service = labels.get("service") or labels.get("job") or "unknown-service"
        lines.append(f"- ({severity}) {service}: {summary}")
        if description:
            lines.append(f"  {description}")

    return {"content": "\n".join(lines)[:1900]}


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        try:
            payload = json.loads(body.decode("utf-8") or "{}")
        except json.JSONDecodeError:
            self.send_response(400)
            self.end_headers()
            self.wfile.write(b"invalid json")
            return

        webhook = os.getenv("DISCORD_WEBHOOK_URL", "").strip()
        if not webhook:
            print("DISCORD_WEBHOOK_URL is not set; alert received but not forwarded.", file=sys.stderr)
            print(json.dumps(payload), file=sys.stderr)
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"no webhook configured")
            return

        message = json.dumps(build_message(payload)).encode("utf-8")
        req = request.Request(
            webhook,
            data=message,
            headers={
                "Content-Type": "application/json",
                "User-Agent": "Banka-Discord-Notifier/1.0",
            },
            method="POST",
        )
        try:
            with request.urlopen(req, timeout=10) as resp:
                _ = resp.read()
        except Exception as exc:
            print(f"failed to forward alert to Discord: {exc}", file=sys.stderr)
            self.send_response(502)
            self.end_headers()
            self.wfile.write(str(exc).encode("utf-8"))
            return

        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"forwarded")

    def log_message(self, format, *args):
        return


if __name__ == "__main__":
    port = int(os.getenv("PORT", "8080"))
    server = HTTPServer(("", port), Handler)
    print(f"discord notifier listening on {port}")
    server.serve_forever()
