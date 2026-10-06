"""TTS local Kokoro-82M (Apache-2.0) via kokoro-onnx (MIT).

Threads do onnxruntime = TTS_THREADS (igual ao limite de CPU do container):
sem isso ele enxerga todas as CPUs do host e perde tempo disputando 2.
"""
import os

import numpy as np
import onnxruntime as ort
from kokoro_onnx import Kokoro

from common_server import serve

VOICES = ["pf_dora", "pm_alex", "pm_santa"]  # pt-BR (VOICES.md do Kokoro)
opts = ort.SessionOptions()
opts.intra_op_num_threads = int(os.environ.get("TTS_THREADS", "2"))
opts.inter_op_num_threads = 1
session = ort.InferenceSession("/models/kokoro-v1.0.onnx", sess_options=opts, providers=["CPUExecutionProvider"])
k = Kokoro.from_session(session, "/models/voices-v1.0.bin")


ALL = set(k.get_voices())


def parse_blend(spec):
    """'pf_dora*0.6+if_sara*0.4' -> [(nome, peso)]; nome sozinho = peso 1."""
    parts = []
    for term in spec.split("+"):
        name, _, w = term.strip().partition("*")
        parts.append((name.strip(), float(w) if w else 1.0))
    return parts


def valid(spec):
    try:
        parts = parse_blend(spec)
    except ValueError:
        return False
    if any(n not in ALL for n, _ in parts):
        return False
    total = sum(w for _, w in parts)
    pt = sum(w for n, w in parts if n in VOICES)
    # Mistura: a parte pt-BR precisa ser pelo menos metade (pronúncia inteligível).
    return total > 0 and pt / total >= 0.5


def style(spec):
    parts = parse_blend(spec)
    if len(parts) == 1:
        return parts[0][0]
    total = sum(w for _, w in parts)
    return sum(k.get_voice_style(n) * (w / total) for n, w in parts).astype(np.float32)


def synth(text, voice, req):
    return k.create(text, voice=style(voice), speed=float(req.get("speed") or 1.0), lang="pt-br")


serve(lambda: VOICES, lambda: {"engine": "kokoro-82m v1.0 (onnx)", "license": "Apache-2.0", "blends": "pf_dora*0.6+if_sara*0.4"}, synth, validate=valid)
