BINARY := csilvmctl
MAINMODULE := github.com/metal-stack/csilvmctl
COMMONDIR := $(or ${COMMONDIR},../builder)

include $(COMMONDIR)/Makefile.inc

all:: platforms
