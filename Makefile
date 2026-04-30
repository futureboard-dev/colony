BIN := colony
INSTALL_DIR := $(HOME)/.local/bin

build:
	go build -o $(BIN) .

install: build
	ln -sf $(PWD)/$(BIN) $(INSTALL_DIR)/$(BIN)

.PHONY: build install
