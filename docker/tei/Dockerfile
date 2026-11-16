# Model files are baked in so the task never downloads from Hugging Face at startup.
FROM alpine:3.22 AS fetch
ARG MODEL
ARG REVISION
ARG FILES="config.json model.safetensors tokenizer.json tokenizer.model tokenizer_config.json special_tokens_map.json modules.json sentence_bert_config.json config_sentence_transformers.json 1_Pooling/config.json"
RUN apk add --no-cache curl
WORKDIR /data/${MODEL}
RUN set -eu; base="https://huggingface.co/cl-nagoya/${MODEL}/resolve/${REVISION}"; \
    for f in ${FILES}; do \
      mkdir -p "$(dirname "$f")"; curl -fsSL -o "$f" "$base/$f"; done

FROM ghcr.io/huggingface/text-embeddings-inference@sha256:798cd7615e060a72ccbdf19aa0ada52289c6a0147490195bafaece6ce04d70f1
ARG MODEL
ENV MODEL_ID=/data/${MODEL}
# The default of 16384 makes the warm-up batch exceed 8GiB and get OOM-killed for 130m and 310m.
ENV MAX_BATCH_TOKENS=4096
COPY --from=fetch /data /data
