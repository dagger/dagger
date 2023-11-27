package main

// inlined s3 cloudformation template as a string
template: """
	  {
	    "AWSTemplateFormatVersion": "2010-09-09",
	    "Resources": {
	      "S3Bucket": {
	        "Type": "AWS::S3::Bucket",
	        "Properties": {
	          "AccessControl": "PublicRead",
	          "WebsiteConfiguration": {
	            "IndexDocument": "index.html",
	            "ErrorDocument": "error.html"
	          }
	        },
	        "DeletionPolicy": "Retain"
	      },
	      "BucketPolicy": {
	        "Type": "AWS::S3::BucketPolicy",
	        "Properties": {
	          "PolicyDocument": {
	            "Id": "MyPolicy",
	            "Version": "2012-10-17",
	            "Statement": [
	              {
	                "Sid": "PublicReadForGetBucketObjects",
	                "Effect": "Allow",
	                "Principal": "*",
	                "Action": "s3:GetObject",
	                "Resource": {
	                  "Fn::Join": [
	                    "",
	                    [
	                      "arn:aws:s3:::",
	                      {
	                        "Ref": "S3Bucket"
	                      },
	                      "/*"
	                    ]
	                  ]
	                }
	              }
	            ]
	          },
	          "Bucket": {
	            "Ref": "S3Bucket"
	          }
	        }
	      }
	    },
	    "Outputs": {
	      "Name": {
	        "Value": {
	          "Fn::GetAtt": ["S3Bucket", "Arn"]
	        },
	        "Description": "Name S3 Bucket"
	      }
	    }
	  }
	"""
