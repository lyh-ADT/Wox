.PHONY: clean build help

# Determine the current platform
ifeq ($(OS),Windows_NT)
    FLUTTER_TARGET := windows
else
    UNAME_S := $(shell uname -s)
    ifeq ($(UNAME_S),Linux)
        FLUTTER_TARGET := linux
    endif
    ifeq ($(UNAME_S),Darwin)
        FLUTTER_TARGET := macos
    endif
endif

UI_FLUTTER_DIR := ../../wox.core/resource/ui/flutter

help:
	@echo "Available commands:"
	@echo "  make clean      - Clean build artifacts"
	@echo "  make build      - Build Flutter UI for current platform ($(FLUTTER_TARGET))"

clean:
	flutter clean
	python -c "import shutil; shutil.rmtree('$(UI_FLUTTER_DIR)', ignore_errors=True)"

build: clean
	flutter build $(FLUTTER_TARGET)
	python -c "import os; os.makedirs('$(UI_FLUTTER_DIR)', exist_ok=True)"
ifeq ($(FLUTTER_TARGET),windows)
	python -c "import shutil; shutil.copytree('build/$(FLUTTER_TARGET)/x64/runner/Release', '$(UI_FLUTTER_DIR)/wox')"
endif
ifeq ($(FLUTTER_TARGET),linux)
	cp -r build/$(FLUTTER_TARGET)/x64/release/bundle $(UI_FLUTTER_DIR)/wox
endif
ifeq ($(FLUTTER_TARGET),macos)
	cp -r build/$(FLUTTER_TARGET)/Build/Products/Release/wox-ui.app $(UI_FLUTTER_DIR)
	chmod +x $(UI_FLUTTER_DIR)/wox-ui.app/Contents/MacOS/wox-ui
endif