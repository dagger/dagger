if filename.startswith(b"sdk/go/e2e/"):
    # The client-library e2e harness depends on the Dagger monorepo and uses a
    # released client as test infrastructure. Do not publish it as part of the
    # standalone Go client-library repository.
    return None

tmpfile = os.path.basename(filename)
if tmpfile != b"go.mod":
    return (filename, mode, blob_id)  # no changes

contents = value.get_contents_by_identifier(blob_id)
with open(tmpfile, "wb") as f:
    f.write(contents)
subprocess.check_call(
    ["go", "mod", "edit", "-dropreplace", "github.com/dagger/dagger", tmpfile]
)
with open(tmpfile, "rb") as f:
    contents = f.read()
new_blob_id = value.insert_file_with_contents(contents)

return (filename, mode, new_blob_id)
