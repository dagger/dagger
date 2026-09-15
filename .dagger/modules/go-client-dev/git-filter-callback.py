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
