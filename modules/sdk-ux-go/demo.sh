#!/bin/sh
set -eu

demo_root=/src/sdk-module-max-demo
mkdir -p "$demo_root/app"
if [ ! -f "$demo_root/app/sdk-ux-go/dagger.json" ]; then
	cp -R /src/modules/sdk-ux-go "$demo_root/app/sdk-ux-go"
fi

if [ ! -f "$demo_root/app/dagger.toml" ]; then
	cat >"$demo_root/app/dagger.toml" <<'EOF'
# The install command adds the module and SDK entries.
EOF
fi
if [ ! -f "$demo_root/app/go.mod" ]; then
	cat >"$demo_root/app/go.mod" <<'EOF'
module example.com/sdk-module-max-demo

go 1.25
EOF
fi

cd "$demo_root/app"
pwd
ls -la
sed -n '1,80p' dagger.toml
dagger -W "$demo_root/app" module install --name go-sdk ./sdk-ux-go
sed -n '1,80p' dagger.toml
dagger -W "$demo_root/app" workspace config-file
dagger -W "$demo_root/app" module init go --help
dagger -W "$demo_root/app" -y module init go
dagger -W "$demo_root/app" -y module init go --path=target --starter=empty
grep -q 'name = "app-dev"' "$demo_root/app/dagger.toml"
grep -q 'name = "target"' "$demo_root/app/dagger.toml"
dagger -W "$demo_root/app/.dagger/modules/app-dev" module client scope
dagger -W "$demo_root/app/.dagger/modules/app-dev" -y module client add ../../../target
sed -n '1,160p' dagger.toml
dagger -W "$demo_root/app/.dagger/modules/app-dev" module client list
dagger -W "$demo_root/app" generate -l

rm -f .dagger/modules/app-dev/internal/dagger/sdk-module-max.gen.txt
rm -f .dagger/modules/app-dev/internal/dagger/clients/target.gen.go
rm -f target/internal/dagger/sdk-module-max.gen.txt
dagger -W "$demo_root/app" -y generate
test -f .dagger/modules/app-dev/internal/dagger/sdk-module-max.gen.txt
test -f .dagger/modules/app-dev/internal/dagger/clients/target.gen.go
test -f target/internal/dagger/sdk-module-max.gen.txt
grep -q 'for app-dev' .dagger/modules/app-dev/internal/dagger/sdk-module-max.gen.txt
grep -q 'for target' target/internal/dagger/sdk-module-max.gen.txt

cp dagger.toml /tmp/sdk-module-max-dagger.toml
if dagger -W "$demo_root/app/target" -y module client add ../.dagger/modules/app-dev >/tmp/sdk-module-max-cycle-error.txt 2>&1; then
	echo "expected local SDK generation cycle" >&2
	exit 1
fi
grep -q "local SDK generation cycle" /tmp/sdk-module-max-cycle-error.txt
cmp dagger.toml /tmp/sdk-module-max-dagger.toml

dagger -W "$demo_root/app/.dagger/modules/app-dev" -y module client rm target
test ! -e .dagger/modules/app-dev/internal/dagger/clients/target.gen.go

find "$demo_root" -maxdepth 5 -type f -print
