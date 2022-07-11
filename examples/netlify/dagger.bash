SITE_NAME=$(dagger input --Site | dagger read-string)
TOKEN=$(dagger input --Token | dagger read-secret)
# SITE_CONTENTS=$(dagger input --Contents)

# TODO: sugar for getting certain output fields, i.e. --output .FS?
# TODO: input corner cases such as when an arg has '--' in it
# TODO: should be able to more flexibly specify package, not just image
# TODO: should be able to skip "core" and instead use optional -p val
FS=$(dagger do localhost:5555/dagger:alpine build --packages curl jq npm | dagger get-field root)
FS=$(dagger do localhost:5555/dagger:core exec --fs "$FS" --dir / --args sh -c "npm -g install netlify-cli@8.6.21" | dagger get-field fs)

dagger do localhost:5555/dagger:core exec --fs "$FS" --dir /src --args sh -c "
echo $SITE_NAME > /sitename
echo $TOKEN > /token
netlify --version > /netlify.version
" | dagger get-field fs | dagger output --fs

# # TODO: handle site creation when it doesn't exist
# netlify link --name $SITE_NAME
# # TODO: gotta be a less ugly way to return data than writing to a file... may require automatically capturing stdout/stderr
# netlify deploy --build --site=$SITE_NAME --prod --json > /output/output.json

