# Setup name variables for the package/tool
NAME := golint-fixer
PKG := github.com/azillion/$(NAME)

CGO_ENABLED := 0

# Set any default go build tags.
BUILDTAGS :=

include basic.mk

.PHONY: prebuild
prebuild:
