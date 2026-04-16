.PHONY: build install uninstall clean app

BINARY=paperclip
APP_NAME=Paperclip.app
INSTALL_PATH ?= $(HOME)/bin
LAUNCHD_PLIST=~/Library/LaunchAgents/com.github.mindmorass.paperclip.plist

build:
	CGO_ENABLED=1 go build -ldflags="-s -w" -o $(BINARY) .

app: build
	@echo "Creating $(APP_NAME)..."
	@rm -rf $(APP_NAME)
	@mkdir -p $(APP_NAME)/Contents/MacOS
	@mkdir -p $(APP_NAME)/Contents/Resources
	@echo '#!/bin/sh' > $(APP_NAME)/Contents/MacOS/paperclip-wrapper
	@echo 'DIR=$$(dirname "$$0")' >> $(APP_NAME)/Contents/MacOS/paperclip-wrapper
	@echo 'exec "$$DIR/$(BINARY)" --tray "$$@"' >> $(APP_NAME)/Contents/MacOS/paperclip-wrapper
	@chmod +x $(APP_NAME)/Contents/MacOS/paperclip-wrapper
	@cp $(BINARY) $(APP_NAME)/Contents/MacOS/$(BINARY)
	@echo '<?xml version="1.0" encoding="UTF-8"?>' > $(APP_NAME)/Contents/Info.plist
	@echo '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' >> $(APP_NAME)/Contents/Info.plist
	@echo '<plist version="1.0">' >> $(APP_NAME)/Contents/Info.plist
	@echo '<dict>' >> $(APP_NAME)/Contents/Info.plist
	@echo '    <key>CFBundleExecutable</key>' >> $(APP_NAME)/Contents/Info.plist
	@echo '    <string>paperclip-wrapper</string>' >> $(APP_NAME)/Contents/Info.plist
	@echo '    <key>CFBundleIdentifier</key>' >> $(APP_NAME)/Contents/Info.plist
	@echo '    <string>com.github.mindmorass.paperclip</string>' >> $(APP_NAME)/Contents/Info.plist
	@echo '    <key>CFBundleName</key>' >> $(APP_NAME)/Contents/Info.plist
	@echo '    <string>Paperclip</string>' >> $(APP_NAME)/Contents/Info.plist
	@echo '    <key>CFBundleVersion</key>' >> $(APP_NAME)/Contents/Info.plist
	@echo '    <string>0.1.0</string>' >> $(APP_NAME)/Contents/Info.plist
	@echo '    <key>LSUIElement</key>' >> $(APP_NAME)/Contents/Info.plist
	@echo '    <true/>' >> $(APP_NAME)/Contents/Info.plist
	@echo '</dict>' >> $(APP_NAME)/Contents/Info.plist
	@echo '</plist>' >> $(APP_NAME)/Contents/Info.plist
	@echo "Built $(APP_NAME) (LSUIElement=true, no dock icon)"

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
