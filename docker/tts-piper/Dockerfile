# tts-piper: só para o benchmark (vozes pt_BR não têm licença comercial).
FROM python:3.12-slim
ENV PIP_NO_CACHE_DIR=1 PYTHONUNBUFFERED=1
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates && rm -rf /var/lib/apt/lists/*
RUN pip install numpy soundfile==0.13.1
RUN curl -fsSL https://github.com/rhasspy/piper/releases/download/2023.11.14-2/piper_linux_x86_64.tar.gz | tar -xz -C /opt
WORKDIR /models
ARG HFV=https://huggingface.co/rhasspy/piper-voices/resolve/main/pt/pt_BR/faber/medium
RUN curl -fsSL -o pt_BR-faber-medium.onnx $HFV/pt_BR-faber-medium.onnx && curl -fsSL -o pt_BR-faber-medium.onnx.json $HFV/pt_BR-faber-medium.onnx.json
COPY common_server.py tts-piper/server.py /app/
RUN useradd -r -u 10005 tts
USER tts
WORKDIR /app
EXPOSE 8080
CMD ["python", "/app/server.py"]
