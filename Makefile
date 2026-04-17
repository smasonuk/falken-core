EMBEDDED_TOOLS_SOURCE_DIR := ./extensions/tools
EMBEDDED_PLUGINS_SOURCE_DIR := ./extensions/plugins
EMBEDDED_ASSET_DIR := ./internal/extensions/embedded_assets
EXTERNAL_PLUGIN_DIR := ./plugins
EXTERNAL_TOOL_DIR := ./tools

# Tools and Plugins
DEFAULT_TOOLS := $(filter-out $(EMBEDDED_TOOLS_SOURCE_DIR)/external, $(wildcard $(EMBEDDED_TOOLS_SOURCE_DIR)/*))
DEFAULT_PLUGINS := $(filter-out $(EMBEDDED_PLUGINS_SOURCE_DIR)/external, $(wildcard $(EMBEDDED_PLUGINS_SOURCE_DIR)/*))
EXTERNAL_PLUGINS := $(wildcard $(EXTERNAL_PLUGIN_DIR)/*)
EXTERNAL_TOOLS := $(wildcard $(EXTERNAL_TOOL_DIR)/*)

.PHONY: all clean core examples default_tools default_plugins plugins tools sync_embedded_assets validate_extensions $(DEFAULT_TOOLS) $(DEFAULT_PLUGINS) $(EXTERNAL_PLUGINS) $(EXTERNAL_TOOLS)

all: default_tools default_plugins sync_embedded_assets core examples plugins tools

core:
	go build ./pkg/...

examples:
	go build ./examples/headless

default_tools: $(DEFAULT_TOOLS)

default_plugins: $(DEFAULT_PLUGINS)

plugins: $(EXTERNAL_PLUGINS)

tools: $(EXTERNAL_TOOLS)

sync_embedded_assets: default_tools default_plugins
	@# Generated output only: extensions/... is canonical, internal/extensions/embedded_assets/... is derived.
	@rm -rf $(EMBEDDED_ASSET_DIR)/tools $(EMBEDDED_ASSET_DIR)/plugins
	@mkdir -p $(EMBEDDED_ASSET_DIR)/tools
	@mkdir -p $(EMBEDDED_ASSET_DIR)/plugins
	@# Sync Tools
	@for dir in $(DEFAULT_TOOLS); do \
		if [ -d $$dir ]; then \
			name=$$(basename $$dir); \
			if [ -f $$dir/tool.yaml ] && [ -f $$dir/main.wasm ]; then \
				mkdir -p $(EMBEDDED_ASSET_DIR)/tools/$$name; \
				cp $$dir/tool.yaml $(EMBEDDED_ASSET_DIR)/tools/$$name/tool.yaml; \
				cp $$dir/main.wasm $(EMBEDDED_ASSET_DIR)/tools/$$name/main.wasm; \
			fi; \
		fi; \
	done
	@# Sync Plugins
	@for dir in $(DEFAULT_PLUGINS); do \
		if [ -d $$dir ]; then \
			name=$$(basename $$dir); \
			if [ -f $$dir/plugin.yaml ] && [ -f $$dir/main.wasm ]; then \
				mkdir -p $(EMBEDDED_ASSET_DIR)/plugins/$$name; \
				cp $$dir/plugin.yaml $(EMBEDDED_ASSET_DIR)/plugins/$$name/plugin.yaml; \
				cp $$dir/main.wasm $(EMBEDDED_ASSET_DIR)/plugins/$$name/main.wasm; \
			fi; \
		fi; \
	done

validate_extensions: sync_embedded_assets
	@GOCACHE=$${GOCACHE:-/tmp/falken-go-cache} go test ./internal/extensions

$(DEFAULT_TOOLS) $(DEFAULT_PLUGINS) $(EXTERNAL_PLUGINS) $(EXTERNAL_TOOLS):
	@if [ -d $@ ]; then \
		echo "Building Wasm extension: $@"; \
		if [ -f $@/go.mod ]; then \
			( cd $@ && GOWORK=off GOTOOLCHAIN=go1.25.0 tinygo build -o main.wasm -target wasi -tags=wasm . ) || \
			echo "Warning: tinygo compilation failed, skipping plugin $@"; \
		else \
			GOTOOLCHAIN=go1.25.0 tinygo build -o $@/main.wasm -target wasi -tags=wasm ./$@ || \
			echo "Warning: tinygo compilation failed, skipping plugin $@"; \
		fi; \
	fi

clean:
	rm -rf $(EMBEDDED_ASSET_DIR)
	find $(EMBEDDED_TOOLS_SOURCE_DIR) -name "*.wasm" -type f -delete
	find $(EMBEDDED_PLUGINS_SOURCE_DIR) -name "*.wasm" -type f -delete
	@if [ -d $(EXTERNAL_PLUGIN_DIR) ]; then find $(EXTERNAL_PLUGIN_DIR) -name "*.wasm" -type f -delete; fi
	@if [ -d $(EXTERNAL_TOOL_DIR) ]; then find $(EXTERNAL_TOOL_DIR) -name "*.wasm" -type f -delete; fi

verify_ui_core_boundary:
	bash ./scripts/verify_ui_core_boundary.sh
