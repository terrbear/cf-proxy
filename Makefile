
run:
	go run main.go

test:
	aws --endpoint-url http://localhost:8080 \
		cloudformation deploy \
		--stack-name blarg \
		--template-file sample.json \
		--no-fail-on-empty-changeset 

build:
	go build -o bin/cf-proxy main.go

docker:
	docker build -t terrbear/cf-proxy .

publish:
	docker push terrbear/cf-proxy