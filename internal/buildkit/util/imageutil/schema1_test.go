package imageutil

import (
	"bytes"
	"testing"
)

func TestConvertSchema1ConfigMeta(t *testing.T) {
	dt := []byte(`{
	"schemaVersion": 1,
	"name": "base-global/common",
	"tag": "1.0.9.zb.standard",
	"architecture": "amd64",
	"fsLayers": [
		{
			"blobSum": "sha256:id1"
		}
	],
	"history": [
		{
			"v1Compatibility": "{\"id\":\"id2\",\"parent\":\"id3\",\"created\":\"2018-07-26T11:56:23.157525618Z\",\"config\":{\"Hostname\":\"4f3d4451\",\"Env\":[\"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\"],\"Cmd\":null,\"Volumes\":{},\"OnBuild\":[\"ARG APP_NAME\",\"COPY ${APP_NAME}.tgz /home/admin/${APP_NAME}/target/${APP_NAME}.tgz\"],\"Labels\":{}},\"architecture\":\"amd64\",\"os\":\"linux\"}"
		}
	]
}`)
	result, err := convertSchema1ConfigMeta(dt)
	if err != nil {
		t.Errorf("convertSchema1ConfigMeta error %v", err)
		return
	}
	if !bytes.Contains(result, []byte("OnBuild")) {
		t.Errorf("convertSchema1ConfigMeta lost onbuild")
	} else if !bytes.Contains(result, []byte("COPY ${APP_NAME}.tgz /home/admin/${APP_NAME}/target/${APP_NAME}.tg")) {
		t.Errorf("convertSchema1ConfigMeta lost onbuild content")
	}
}
