"""Servidor HTTP comum aos TTS locais: POST /synthesize -> WAV PCM16 mono.

Cada imagem define engine_voices(), engine_info() e engine_synth(text, voice, opts)
-> (np.float32 samples, sr). Textos longos são divididos em frases e unidos com
uma pausa curta. Uma síntese por vez (limita CPU e memória).
"""
import io
import json
import re
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import numpy as np
import soundfile as sf

LOCK = threading.Lock()
MAX_CHUNK = 240


def chunks(text):
    parts, cur = [], ""
    for s in re.split(r"(?<=[.!?…])\s+", text.strip()):
        if cur and len(cur) + len(s) + 1 > MAX_CHUNK:
            parts.append(cur)
            cur = s
        else:
            cur = (cur + " " + s).strip()
    if cur:
        parts.append(cur)
    out = []
    for p in parts:  # frase gigante sem pontuação: corta por vírgula/espaço
        while len(p) > MAX_CHUNK:
            cut = max(p.rfind(",", 0, MAX_CHUNK), p.rfind(" ", 0, MAX_CHUNK))
            cut = cut if cut > 40 else MAX_CHUNK
            out.append(p[:cut + 1].strip())
            p = p[cut + 1:].strip()
        if p:
            out.append(p)
    return out


def serve(engine_voices, engine_info, engine_synth, port=8080):
    class Handler(BaseHTTPRequestHandler):
        def _json(self, code, obj):
            body = json.dumps(obj).encode()
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self):
            if self.path == "/healthz":
                return self._json(200, {"ok": True})
            if self.path == "/voices":
                return self._json(200, dict(engine_info(), voices=engine_voices()))
            self._json(404, {"error": "not found"})

        def do_POST(self):
            if self.path != "/synthesize":
                return self._json(404, {"error": "not found"})
            try:
                req = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
                text, voice = req["text"], req.get("voice", "")
                if voice not in engine_voices():
                    return self._json(400, {"error": f"voz {voice!r} indisponível"})
                pieces, sr = [], None
                with LOCK:
                    for c in chunks(text):
                        samples, sr = engine_synth(c, voice, req)
                        pieces.append(np.asarray(samples, dtype=np.float32).reshape(-1))
                        pieces.append(np.zeros(int(sr * 0.15), dtype=np.float32))
                audio = np.concatenate(pieces[:-1]) if pieces else np.zeros(1, dtype=np.float32)
                buf = io.BytesIO()
                sf.write(buf, audio, sr, format="WAV", subtype="PCM_16")
                body = buf.getvalue()
                self.send_response(200)
                self.send_header("Content-Type", "audio/wav")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            except Exception as e:  # noqa: BLE001
                self._json(500, {"error": str(e)})

        def log_message(self, fmt, *args):
            pass

    ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
