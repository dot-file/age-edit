.PHONY: all
all: age-edit

age-edit: main.go
	CGO_ENABLED=0 go build -trimpath

.PHONY: clean
clean:
	-rm age-edit

.PHONY: test
test: age-edit
	go test
