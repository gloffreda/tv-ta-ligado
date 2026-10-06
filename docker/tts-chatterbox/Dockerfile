# tts-chatterbox: Chatterbox Multilingual V3 + finetune pt-BR (Resemble AI, MIT). Só CPU.
FROM python:3.10-slim
ENV PIP_NO_CACHE_DIR=1 PYTHONUNBUFFERED=1 HF_HOME=/models
RUN apt-get update && apt-get install -y --no-install-recommends libsndfile1 ca-certificates && rm -rf /var/lib/apt/lists/*
RUN pip install --index-url https://download.pytorch.org/whl/cpu --extra-index-url https://pypi.org/simple torch==2.8.0 torchaudio==2.8.0
RUN pip install numpy==1.26.0 resampy==0.4.3 librosa==0.10.0 s3tokenizer transformers==4.46.3 diffusers==0.29.0 \
    omegaconf==2.3.0 resemble-perth==1.0.1 silero-vad==5.1.2 conformer==0.3.2 safetensors soundfile huggingface_hub
# Código de inferência da Space oficial pt-BR (o mesmo que a demo usa).
RUN python -c "from huggingface_hub import snapshot_download; snapshot_download('ResembleAI/Chatterbox-Multilingual-TTS-pt-br', repo_type='space', local_dir='/app', allow_patterns=['chatterbox/**'])" \
 && touch /app/chatterbox/__init__.py /app/chatterbox/src/__init__.py
# Pesos: base (ve, tokenizer, voz padrão) + finetune pt-BR (t3, s3gen).
RUN python -c "\
from huggingface_hub import snapshot_download, hf_hub_download; \
snapshot_download('ResembleAI/chatterbox', allow_patterns=['ve.pt','grapheme_mtl_merged_expanded_v1.json','conds.pt']); \
[hf_hub_download('ResembleAI/Chatterbox-Multilingual-pt-br', f) for f in ['t3_pt_br.safetensors','s3gen_v3.pt']]"
COPY common_server.py tts-chatterbox/server.py /app/
RUN useradd -r -u 10004 tts && chown -R tts /models
USER tts
ENV HF_HUB_OFFLINE=1 NUMBA_CACHE_DIR=/tmp/numba MPLCONFIGDIR=/tmp/mpl
WORKDIR /app
EXPOSE 8080
CMD ["python", "/app/server.py"]
