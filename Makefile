MODEL ?= ruri-v3-30m
REV_ruri-v3-30m := 24899e5de370b56d179604a007c0d727bf144504
REV_ruri-v3-130m := e3114c6ee10dbab8b4b235fbc6dcf9dd4d5ac1a6
REV_ruri-v3-310m := 18b60fb8c2b9df296fb4212bb7d23ef94e579cd3
REV_ruri-v3-reranker-310m := bb46934ee9ed09f850b9fcff17501b3ef7ddb2b3
FILES_ruri-v3-reranker-310m := config.json model.safetensors tokenizer.json tokenizer.model tokenizer_config.json special_tokens_map.json
VENV ?= .venv

.PHONY: tei-image tei-parity
tei-image:
	docker build --platform linux/arm64 --build-arg MODEL=$(MODEL) --build-arg REVISION=$(REV_$(MODEL)) $(if $(FILES_$(MODEL)),--build-arg FILES="$(FILES_$(MODEL))") -t ics-tei:$(MODEL) docker/tei

tei-parity: tei-image
	test -x $(VENV)/bin/python || (uv venv -q $(VENV) --python 3.11 && uv pip install -q --python $(VENV)/bin/python -r scripts/requirements.txt)
	docker rm -f ics-tei-parity >/dev/null 2>&1 || true
	docker run -d --name ics-tei-parity -p 18080:80 ics-tei:$(MODEL) >/dev/null
	for i in $$(seq 1 120); do curl -sf localhost:18080/health >/dev/null && break; sleep 1; done
	curl -sf localhost:18080/health >/dev/null || { docker inspect -f 'exit={{.State.ExitCode}} oom={{.State.OOMKilled}}' ics-tei-parity; docker logs ics-tei-parity 2>&1 | tail -20; docker rm -f ics-tei-parity >/dev/null; exit 1; }
	$(VENV)/bin/python -I scripts/tei_parity.py $(MODEL) http://localhost:18080; rc=$$?; docker rm -f ics-tei-parity >/dev/null; exit $$rc

.PHONY: check
check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	golangci-lint run ./...
	staticcheck ./...
	go test -count=1 ./...
	go build ./...
