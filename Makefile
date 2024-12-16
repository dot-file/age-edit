.PHONY: all
all: age-edit

.PHONY: clean
clean:
	-rm age-edit

age-edit: main.go
	CGO_ENABLED=0 go build -trimpath
