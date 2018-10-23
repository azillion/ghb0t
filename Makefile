# Setup name variables for the package/tool
NAME := ghb0t
PKG := github.com/azillion/$(NAME)

CGO_ENABLED := 0

# Set any default go build tags.
BUILDTAGS :=

include basic.mk

.PHONY: prebuild
prebuild:
