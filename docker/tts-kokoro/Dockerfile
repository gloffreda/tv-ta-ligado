# tts-kokoro: Kokoro-82M (pesos Apache-2.0) via kokoro-onnx (MIT). Só CPU.
FROM python:3.12-slim
ENV PIP_NO_CACHE_DIR=1 PYTHONUNBUFFERED=1
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates && rm -rf /var/lib/apt/lists/*
RUN pip install kokoro-onnx==0.6.1 soundfile==0.13.1
WORKDIR /models
ARG KOKORO_REL=https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0
RUN curl -fsSL -o kokoro-v1.0.onnx ${KOKORO_REL}/kokoro-v1.0.onnx \
 && curl -fsSL -o voices-v1.0.bin ${KOKORO_REL}/voices-v1.0.bin
COPY common_server.py tts-kokoro/server.py /app/
RUN useradd -r -u 10002 tts
USER tts
WORKDIR /app
EXPOSE 8080
CMD ["python", "/app/server.py"]
