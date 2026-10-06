"""TTS local Chatterbox Multilingual V3, finetune pt-BR (Resemble AI, MIT).

Voz: "default" (voz embutida do modelo, conds.pt) ou um arquivo de referência
<nome>.wav em /voices (montado de config/voices, só com direito de uso
comprovado). A marca d'água Perth do modelo é mantida (aplicada em generate()).
"""
import os
import random

import numpy as np
import torch

from common_server import serve
from chatterbox.src.chatterbox.tts import ChatterboxTTS

torch.set_num_threads(int(os.environ.get("TTS_THREADS", "4")))
model = ChatterboxTTS.from_pretrained("cpu")
VOICE_DIR = "/voices"


def voices():
    refs = []
    if os.path.isdir(VOICE_DIR):
        refs = [f[:-4] for f in sorted(os.listdir(VOICE_DIR)) if f.endswith(".wav")]
    return ["default"] + refs


def synth(text, voice, req):
    seed = int(req.get("seed") or 1234)  # voz estável entre falas
    torch.manual_seed(seed)
    random.seed(seed)
    np.random.seed(seed)
    kw = dict(
        language_id="pt",
        exaggeration=float(req.get("exaggeration") or 0.5),
        cfg_weight=float(req.get("cfg_weight") or 0.5),
        temperature=float(req.get("temperature") or 0.8),
    )
    if voice != "default":
        kw["audio_prompt_path"] = os.path.join(VOICE_DIR, voice + ".wav")
    wav = model.generate(text, **kw)
    return wav.squeeze(0).cpu().numpy(), model.sr


serve(voices, lambda: {"engine": "chatterbox-multilingual-v3 pt-br", "license": "MIT", "watermark": "perth"}, synth)
