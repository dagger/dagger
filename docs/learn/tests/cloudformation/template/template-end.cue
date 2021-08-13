package main

import "encoding/json"

s3: #Deployment & {
	description: "Name S3 Bucket"
}

// Template contains the marshalled value of the s3 template
template: json.Marshal(s3.template)
