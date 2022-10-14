// Add this line, to make it part to the cloudformation template
package main

import "encoding/json"

// Wrap exported Cue in previous point inside the `s3` value
s3: {
	AWSTemplateFormatVersion: "2010-09-09"
	Outputs: Name: {
		Description: "Name S3 Bucket"
		Value: "Fn::GetAtt": [
			"S3Bucket",
			"Arn",
		]
	}
	Resources: {
		BucketPolicy: {
			Properties: {
				Bucket: Ref: "S3Bucket"
				PolicyDocument: {
					Id: "MyPolicy"
					Statement: [
						{
							Action:    "s3:GetObject"
							Effect:    "Allow"
							Principal: "*"
							Resource: "Fn::Join": [
								"",
								[
									"arn:aws:s3:::",
									{
										Ref: "S3Bucket"
									},
									"/*",
								],
							]
							Sid: "PublicReadForGetBucketObjects"
						},
					]
					Version: "2012-10-17"
				}
			}
			Type: "AWS::S3::BucketPolicy"
		}
		S3Bucket: {
			DeletionPolicy: "Retain"
			Properties: {
				AccessControl: "PublicRead"
				WebsiteConfiguration: {
					ErrorDocument: "error.html"
					IndexDocument: "index.html"
				}
			}
			Type: "AWS::S3::Bucket"
		}
	}
}

// Template contains the marshalled value of the s3 template
template: json.Marshal(s3)
