.PHONY: build install uninstall clean

BINARY=paperclip
INSTALL_PATH ?= $(HOME)/bin
LAUNCHD_PLIST=~/Library/LaunchAgents/com.github.mindmorass.paperclip.plist

build:
	go build -ldflags="-s -w" -o $(BINARY) .

install: build
	@mkdir -p $(INSTALL_PATH)
	@echo "Installing $(BINARY) to $(INSTALL_PATH)..."
	cp $(BINARY) $(INSTALL_PATH)/$(BINARY)
	@echo "Done. Run '$(BINARY) --help' for usage."
	@echo ""
	@echo "Make sure $(INSTALL_PATH) is in your PATH:"
	@echo "  export PATH=\"$(INSTALL_PATH):\$$PATH\""

launchd:
	@echo "Generating launchd plist..."
	@$(BINARY) --launchd

load:
	@echo "Loading launchd agent..."
	launchctl load $(LAUNCHD_PLIST)

unload:
	@echo "Unloading launchd agent..."
	-launchctl unload $(LAUNCHD_PLIST)

uninstall: unload
	@echo "Removing $(BINARY)..."
	-rm -f $(INSTALL_PATH)/$(BINARY)
	-rm -f $(LAUNCHD_PLIST)
	@echo "Uninstalled."

clean:
	rm -f $(BINARY)
