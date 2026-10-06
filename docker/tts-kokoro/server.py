"""TTS local Kokoro-82M (Apache-2.0) via kokoro-onnx (MIT).

Threads do onnxruntime = TTS_THREADS (igual ao limite de CPU do container):
sem isso ele enxerga todas as CPUs do host e perde tempo disputando 2.
"""
import os

import onnxruntime as ort
from kokoro_onnx import Kokoro

from common_server import serve

VOICES = ["pf_dora", "pm_alex", "pm_santa"]  # pt-BR (VOICES.md do Kokoro)
opts = ort.SessionOptions()
opts.intra_op_num_threads = int(os.environ.get("TTS_THREADS", "2"))
opts.inter_op_num_threads = 1
session = ort.InferenceSession("/models/kokoro-v1.0.onnx", sess_options=opts, providers=["CPUExecutionProvider"])
k = Kokoro.from_session(session, "/models/voices-v1.0.bin")


def synth(text, voice, req):
    return k.create(text, voice=voice, speed=float(req.get("speed") or 1.0), lang="pt-br")


serve(lambda: VOICES, lambda: {"engine": "kokoro-82m v1.0 (onnx)", "license": "Apache-2.0"}, synth)
