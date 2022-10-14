package main

#Deployment: {

	// Bucket's output description
	description: string

	// index file
	indexDocument: *"index.html" | string

	// error file
	errorDocument: *"error.html" | string

	// Bucket policy version
	version: *"2012-10-17" | string

	// Retain as default deletion policy. Delete is also accepted but requires the s3 bucket to be empty
	deletionPolicy: *"Retain" | "Delete"

	// Canned access control list (ACL) that grants predefined permissions to the bucket
	accessControl: *"PublicRead" | "Private" | "PublicReadWrite" | "AuthenticatedRead" | "LogDeliveryWrite" | "BucketOwnerRead" | "BucketOwnerFullControl" | "AwsExecRead"

	// Modified copy of s3 value in `todoapp/cloudformation/template.cue`
	template: {
		AWSTemplateFormatVersion: "2010-09-09"
		Outputs: Name: {
			Description: description
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
						Version: version
					}
				}
				Type: "AWS::S3::BucketPolicy"
			}
			S3Bucket: {
				DeletionPolicy: deletionPolicy
				Properties: {
					AccessControl: "PublicRead"
					WebsiteConfiguration: {
						ErrorDocument: errorDocument
						IndexDocument: indexDocument
					}
				}
				Type: "AWS::S3::Bucket"
			}
		}
	}
}
