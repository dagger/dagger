SHELL := bash# we want bash behaviour in all shell invocations
PLATFORM := $(shell uname)
platform := $(shell uname | tr A-Z a-z)
architecture := $(shell uname -m)
ifeq ($(architecture),x86_64)
architecture_alt := amd64
endif

# https://stackoverflow.com/questions/4842424/list-of-ansi-color-escape-sequences
BOLD := \033[1m
NORMAL := \033[0m
RED := \033[1;31m
GREEN := \033[1;32m

LOCAL_BIN := $(CURDIR)/bin
$(LOCAL_BIN):
	mkdir -p $(LOCAL_BIN)

XDG_CONFIG_HOME ?= $(CURDIR)/.config
export XDG_CONFIG_HOME
.DEFAULT_GOAL := help
HELP_TARGET_DEPTH ?= \#
.PHONY: help
help: # Show how to get started & what targets are available
	@printf "\nIf this is your first time running this, remember to run: $(BOLD)make .env && source .env$(NORMAL)\n"
	@printf "This is a list of all the make targets that you can run, e.g. $(BOLD)make dagger$(NORMAL) - or $(BOLD)m dagger$(NORMAL)\n\n"
	@awk -F':+ |$(HELP_TARGET_DEPTH)' '/^[0-9a-zA-Z._%-]+:+.+$(HELP_TARGET_DEPTH).+$$/ { printf "$(GREEN)%-20s\033[0m %s\n", $$1, $$3 }' $(MAKEFILE_LIST) | sort
	@echo

.PHONY: env
env:: # Print all env variables
	@echo 'alias m=make'
	@echo 'export PATH="$(LOCAL_BIN):$$PATH"'
	@echo 'export XDG_CONFIG_HOME="$(XDG_CONFIG_HOME)"'

.env: # Create the .env file - 💡 use the -B flag to re-create
	$(MAKE) --no-print-directory env > .env

# Every system has curl installed here:
CURL ?= /usr/bin/curl

AGE := $(LOCAL_BIN)/age
AGE_RELEASES := https://github.com/FiloSottile/age/releases
AGE_VERSION := 1.0.0
AGE_BIN_DIR := $(LOCAL_BIN)/age-v$(AGE_VERSION)-$(platform)-$(architecture_alt)
AGE_URL := $(AGE_RELEASES)/download/v$(AGE_VERSION)/$(notdir $(AGE_BIN_DIR)).tar.gz
AGE := $(AGE_BIN_DIR)/age/age
$(AGE): | $(CURL) $(LOCAL_BIN)
	$(CURL) --progress-bar --fail --location --output $(AGE_BIN_DIR).tar.gz "$(AGE_URL)"
	mkdir -p $(AGE_BIN_DIR) && tar zxf $(AGE_BIN_DIR).tar.gz -C $(AGE_BIN_DIR)
	touch $(AGE)
	chmod +x $(AGE)
	$(AGE) --version | grep $(AGE_VERSION)
	ln -sf $(AGE) $(LOCAL_BIN)/age
.PHONY: age
age: $(AGE)

define get_github_keys_for_age_recipient
@printf "Configuring $(BOLD)$(1)$(NORMAL) as an age recipient...\n"
@echo "# $(1)" >> $(@)
@$(CURL) --silent --fail --location $(1) >> $(@)
endef
.age.recipients.txt: | $(CURL) # Generate all AGE recipients from GitHub keys
	$(call get_github_keys_for_age_recipient,https://github.com/aluzzardi.keys)
	$(call get_github_keys_for_age_recipient,https://github.com/gerhard.keys)
	$(call get_github_keys_for_age_recipient,https://github.com/grouville.keys)
	$(call get_github_keys_for_age_recipient,https://github.com/jlongtine.keys)
	$(call get_github_keys_for_age_recipient,https://github.com/samalba.keys)
	$(call get_github_keys_for_age_recipient,https://github.com/shykes.keys)
	$(call get_github_keys_for_age_recipient,https://github.com/slumbering.keys)
	$(call get_github_keys_for_age_recipient,https://github.com/talentedmrjones.keys)

DOCTL := $(LOCAL_BIN)/age
DOCTL_RELEASES := https://github.com/digitalocean/doctl/releases
DOCTL_VERSION := 1.69.0
DOCTL_BIN_DIR := $(LOCAL_BIN)/doctl-$(DOCTL_VERSION)-$(platform)-$(architecture_alt)
DOCTL_URL := $(DOCTL_RELEASES)/download/v$(DOCTL_VERSION)/$(notdir $(DOCTL_BIN_DIR)).tar.gz
DOCTL := $(DOCTL_BIN_DIR)/doctl
$(DOCTL): | $(CURL) $(LOCAL_BIN)
	$(CURL) --progress-bar --fail --location --output $(DOCTL_BIN_DIR).tar.gz "$(DOCTL_URL)"
	mkdir -p $(DOCTL_BIN_DIR) && tar zxf $(DOCTL_BIN_DIR).tar.gz -C $(DOCTL_BIN_DIR)
	touch $(DOCTL)
	chmod +x $(DOCTL)
	$(DOCTL) version | grep $(DOCTL_VERSION)
	ln -sf $(DOCTL) $(LOCAL_BIN)/doctl
.PHONY: doctl
doctl: $(DOCTL)
ifndef DIGITALOCEAN_ACCESS_TOKEN
	@printf "\n$(RED)DIGITALOCEAN_ACCESS_TOKEN $(BOLD)env var is missing$(NORMAL)\n"
	@printf "\nIf your private SSH key is in the $(BOLD).age.recipients.txt$(NORMAL) file, you can do the following:\n"
	@printf "$(BOLD)export DIGITALOCEAN_ACCESS_TOKEN=\$$(age -d -i ~/.ssh/$(GREEN)YOUR_SSH_PRIVATE_KEY$(NORMAL)$(BOLD) .do.dagger-ci-pr1499-2022-01-26.age)$(NORMAL)\n"
	@printf "\nReplace $(BOLD)$(GREEN)YOUR_SSH_PRIVATE_KEY$(NORMAL) with the name of your private SSH key\n\n"
	@exit 1
endif

DAGGER_CI_NAME ?= dagger-ci-2022-01-26
.PHONY: dagger-ci
dagger-ci: | $(AGE) doctl # Create dagger-ci
	$(DOCTL) compute droplet create \
		--image debian-11-x64 \
		--size s-1vcpu-1gb-intel \
		--region nyc1 \
		--enable-monitoring \
		--ssh-keys 32985130,32968299,32835944,23961075,23698535 \
		--user-data-file ./dagger-ci.cloudinit \
		$(DAGGER_CI_NAME)

.PHONY: dagger-ci-ssh
dagger-ci-ssh: | $(AGE) doctl # SSH into dagger-ci
	$(DOCTL) compute ssh $(DAGGER_CI_NAME)
