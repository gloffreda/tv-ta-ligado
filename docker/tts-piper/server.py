"""TTS local Piper (binário MIT 2023.11.14-2). Medição apenas: as vozes pt_BR
publicadas derivam de modelos de licença não comercial (ver DECISIONS.md)."""
import io
import subprocess

import soundfile as sf

from common_server import serve

VOICES = {"faber": "/models/pt_BR-faber-medium.onnx"}


def synth(text, voice, req):
    length = 1.0 / float(req.get("speed") or 1.0)
    out = subprocess.run(["/opt/piper/piper", "--model", VOICES[voice], "--length_scale", str(length), "--output_file", "-"],
                         input=text.encode(), capture_output=True, check=True)
    data, sr = sf.read(io.BytesIO(out.stdout), dtype="float32")
    return data, sr


serve(lambda: list(VOICES), lambda: {"engine": "piper 2023.11.14-2", "license": "MIT (motor); vozes pt_BR NÃO comerciais"}, synth)
