.PHONY: docker

docker:
	docker build -t uppr . && docker run --rm \
		-p 9944:9944 \
		-v $(CURDIR)/config:/app/config \
		-v $(CURDIR)/data:/app/data \
		uppr
