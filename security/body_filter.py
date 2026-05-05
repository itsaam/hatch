#!/usr/bin/env python3
"""
Docker API body-filter proxy for hatch.

Listens on a unix socket; forwards to an upstream HTTP socket-proxy (tecnativa).
Inspects POST /containers/create bodies and rejects dangerous HostConfig
(Privileged, host bind mounts outside allowlist, dangerous caps, host
namespace sharing, devices, runtime override, etc.).

Streams transparently for hijacked endpoints (/exec/start, /attach, /build,
/session) so BuildKit and `docker exec` keep working.
"""
import json
import logging
import os
import re
import select
import socket
import threading
import urllib.parse

UPSTREAM_HOST = os.environ.get("UPSTREAM_HOST", "docker-proxy")
UPSTREAM_PORT = int(os.environ.get("UPSTREAM_PORT", "2375"))
LISTEN_SOCKET = os.environ.get("LISTEN_SOCKET", "/shared/docker.sock")
ALLOWED_HOST_PATHS = [
    p.strip() for p in os.environ.get("ALLOWED_HOST_PATHS", "/etc/hatch/secrets").split(",")
    if p.strip()
]

DANGEROUS_CAPS = {
    "ALL", "SYS_ADMIN", "SYS_PTRACE", "SYS_MODULE", "SYS_RAWIO",
    "SYS_BOOT", "SYS_TIME", "SYSLOG", "MAC_ADMIN", "MAC_OVERRIDE",
    "DAC_READ_SEARCH", "DAC_OVERRIDE", "NET_ADMIN", "AUDIT_CONTROL",
    "AUDIT_READ", "BPF", "PERFMON",
}
FORBIDDEN_HOST_MODES = {"PidMode", "IpcMode", "UsernsMode", "UTSMode", "CgroupnsMode"}

CREATE_PATH_RE = re.compile(r"^(/v[\d\.]+)?/containers/create(\?|$)")


def _path_allowed(host_path: str) -> bool:
    host_path = os.path.normpath(host_path)
    for allowed in ALLOWED_HOST_PATHS:
        ap = os.path.normpath(allowed)
        if host_path == ap or host_path.startswith(ap + "/"):
            return True
    return False


def validate_create(body: bytes):
    """Return None if OK, else a string describing why it's blocked."""
    try:
        data = json.loads(body or b"{}")
    except Exception as e:
        return f"invalid JSON: {e}"

    hc = data.get("HostConfig") or {}

    if hc.get("Privileged") is True:
        return "HostConfig.Privileged=true"

    if hc.get("PublishAllPorts") is True:
        return "HostConfig.PublishAllPorts=true"

    for f in FORBIDDEN_HOST_MODES:
        v = hc.get(f) or ""
        if isinstance(v, str) and (v == "host" or v.startswith("host:")):
            return f"HostConfig.{f}={v}"

    nm = hc.get("NetworkMode") or ""
    if nm == "host":
        return "HostConfig.NetworkMode=host"

    for cap in (hc.get("CapAdd") or []):
        c = (cap or "").upper().replace("CAP_", "")
        if c in DANGEROUS_CAPS:
            return f"HostConfig.CapAdd={cap}"

    if hc.get("Devices"):
        return "HostConfig.Devices set"
    if hc.get("DeviceRequests"):
        return "HostConfig.DeviceRequests set"

    rt = hc.get("Runtime")
    if rt and rt not in ("runc", "io.containerd.runc.v2"):
        return f"HostConfig.Runtime={rt}"

    for g in (hc.get("GroupAdd") or []):
        if str(g) in ("0", "root", "docker"):
            return f"HostConfig.GroupAdd={g}"

    if hc.get("SecurityOpt"):
        for s in hc["SecurityOpt"]:
            if "apparmor=unconfined" in s or "seccomp=unconfined" in s or "no-new-privileges:false" in s:
                return f"HostConfig.SecurityOpt={s}"

    for bind in (hc.get("Binds") or []):
        host_path = bind.split(":", 1)[0]
        if not _path_allowed(host_path):
            return f"HostConfig.Binds host_path={host_path} not in allowlist"

    for m in (hc.get("Mounts") or []):
        if m.get("Type") == "bind":
            src = m.get("Source", "")
            if not _path_allowed(src):
                return f"Mounts bind Source={src} not in allowlist"

    return None


def read_request(sock):
    """Read HTTP request from client socket. Returns (raw_bytes, body_offset, headers_dict)."""
    buf = b""
    while b"\r\n\r\n" not in buf:
        chunk = sock.recv(8192)
        if not chunk:
            return None, None, None
        buf += chunk
        if len(buf) > 1024 * 1024:
            return None, None, None
    head, _, rest = buf.partition(b"\r\n\r\n")
    lines = head.split(b"\r\n")
    request_line = lines[0].decode("iso-8859-1", "replace")
    headers = {}
    for line in lines[1:]:
        if b":" in line:
            k, _, v = line.partition(b":")
            headers[k.decode("iso-8859-1").strip().lower()] = v.decode("iso-8859-1").strip()
    body = rest
    cl = int(headers.get("content-length", 0) or 0)
    while len(body) < cl:
        chunk = sock.recv(min(65536, cl - len(body)))
        if not chunk:
            break
        body += chunk
    return request_line, headers, body, head


def relay(src, dst):
    try:
        while True:
            data = src.recv(65536)
            if not data:
                break
            dst.sendall(data)
    except Exception:
        pass
    finally:
        try:
            dst.shutdown(socket.SHUT_WR)
        except Exception:
            pass


def handle_client(client):
    upstream = None
    try:
        result = read_request(client)
        if not result or result[0] is None:
            return
        request_line, headers, body, head_raw = result

        try:
            method, path, _ = request_line.split(" ", 2)
        except ValueError:
            return

        # Validate create bodies
        if method == "POST" and CREATE_PATH_RE.match(path):
            err = validate_create(body)
            if err:
                logging.warning("BLOCKED %s %s: %s", method, path, err)
                msg = json.dumps({"message": f"Blocked by hatch body-filter: {err}"}).encode()
                resp = (
                    b"HTTP/1.1 403 Forbidden\r\n"
                    b"Content-Type: application/json\r\n"
                    b"Content-Length: " + str(len(msg)).encode() + b"\r\n"
                    b"Connection: close\r\n\r\n"
                ) + msg
                client.sendall(resp)
                return

        # Connect upstream and forward request as-is
        upstream = socket.create_connection((UPSTREAM_HOST, UPSTREAM_PORT), timeout=120)
        upstream.sendall(head_raw + b"\r\n\r\n" + body)

        # For hijacked / streaming endpoints, do bidirectional relay
        # Easier: always do bidirectional relay after sending request.
        # client and upstream run in two threads.
        t = threading.Thread(target=relay, args=(client, upstream), daemon=True)
        t.start()
        relay(upstream, client)
        t.join(timeout=1)
    except Exception as e:
        logging.debug("handler error: %s", e)
    finally:
        for s in (client, upstream):
            try:
                if s:
                    s.close()
            except Exception:
                pass


def main():
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
    if os.path.exists(LISTEN_SOCKET):
        os.unlink(LISTEN_SOCKET)
    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    srv.bind(LISTEN_SOCKET)
    os.chmod(LISTEN_SOCKET, 0o660)
    srv.listen(64)
    logging.info(
        "hatch body-filter listening on %s -> %s:%d (allowlist=%s)",
        LISTEN_SOCKET, UPSTREAM_HOST, UPSTREAM_PORT, ALLOWED_HOST_PATHS,
    )
    while True:
        try:
            cli, _ = srv.accept()
        except Exception as e:
            logging.error("accept failed: %s", e)
            continue
        threading.Thread(target=handle_client, args=(cli,), daemon=True).start()


if __name__ == "__main__":
    main()
