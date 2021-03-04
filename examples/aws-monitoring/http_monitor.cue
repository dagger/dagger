package main

import (
	"strings"
	"regexp"
	"encoding/json"

	"dagger.io/aws"
	"dagger.io/aws/cloudformation"
)

#Notification: {
	protocol: string
	endpoint: string
}

#Canary: {
	name:               =~"^[0-9a-z_-]{1,21}$"
	slug:               strings.Join(regexp.FindAll("[0-9a-zA-Z]*", name, -1), "")
	url:                string
	expectedHTTPCode:   *200 | int
	timeoutInSeconds:   *30 | int
	intervalExpression: *"1 minute" | string
}

#HTTPMonitor: {

	// For sending notifications
	notifications: [...#Notification]
	// Canaries (tests)
	canaries: [...#Canary]
	// Name of the Cloudformation stack
	cfnStackName: string
	// AWS Config
	awsConfig: aws.#Config

	cfnStack: cloudformation.#Stack & {
		config:    awsConfig
		source:    json.Marshal(#cfnTemplate)
		stackName: cfnStackName
		onFailure: "DO_NOTHING"
	}

	// Function handler
	#lambdaHandler: {
		url:              string
		expectedHTTPCode: int

		script: #"""
			var synthetics = require('Synthetics');
			const log = require('SyntheticsLogger');

			const pageLoadBlueprint = async function () {

				// INSERT URL here
				const URL = "\#(url)";

				let page = await synthetics.getPage();
				const response = await page.goto(URL, {waitUntil: 'domcontentloaded', timeout: 30000});
				//Wait for page to render.
				//Increase or decrease wait time based on endpoint being monitored.
				await page.waitFor(15000);
				// This will take a screenshot that will be included in test output artifacts
				await synthetics.takeScreenshot('loaded', 'loaded');
				let pageTitle = await page.title();
				log.info('Page title: ' + pageTitle);
				if (response.status() !== \#(expectedHTTPCode)) {
					throw "Failed to load page!";
				}
			};

			exports.handler = async () => {
				return await pageLoadBlueprint();
			};
			"""#
	}

	#cfnTemplate: {
		AWSTemplateFormatVersion: "2010-09-09"
		Description:              "CloudWatch Synthetics website monitoring"
		Resources: {
			Topic: {
				Type: "AWS::SNS::Topic"
				Properties: Subscription: [
					for e in notifications {
						Endpoint: e.endpoint
						Protocol: e.protocol
					},
				]
			}
			TopicPolicy: {
				Type: "AWS::SNS::TopicPolicy"
				Properties: {
					PolicyDocument: {
						Id:      "Id1"
						Version: "2012-10-17"
						Statement: [
							{
								Sid:    "Sid1"
								Effect: "Allow"
								Principal: AWS: "*"
								Action: "sns:Publish"
								Resource: Ref: "Topic"
								Condition: StringEquals: "AWS:SourceOwner": Ref: "AWS::AccountId"
							},
						]
					}
					Topics: [
						{
							Ref: "Topic"
						},
					]
				}
			}
			CanaryBucket: {
				Type: "AWS::S3::Bucket"
				Properties: {}
			}
			CanaryRole: {
				Type: "AWS::IAM::Role"
				Properties: {
					AssumeRolePolicyDocument: {
						Version: "2012-10-17"
						Statement: [
							{
								Effect: "Allow"
								Principal: Service: "lambda.amazonaws.com"
								Action: "sts:AssumeRole"
							},
						]
					}
					Policies: [
						{
							PolicyName: "execution"
							PolicyDocument: {
								Version: "2012-10-17"
								Statement: [
									{
										Effect:   "Allow"
										Action:   "s3:ListAllMyBuckets"
										Resource: "*"
									},
									{
										Effect: "Allow"
										Action: "s3:PutObject"
										Resource: "Fn::Sub": "${CanaryBucket.Arn}/*"
									},
									{
										Effect: "Allow"
										Action: "s3:GetBucketLocation"
										Resource: "Fn::GetAtt": [
											"CanaryBucket",
											"Arn",
										]
									},
									{
										Effect:   "Allow"
										Action:   "cloudwatch:PutMetricData"
										Resource: "*"
										Condition: StringEquals: "cloudwatch:namespace": "CloudWatchSynthetics"
									},
								]
							}
						},
					]
				}
			}
			CanaryLogGroup: {
				Type: "AWS::Logs::LogGroup"
				Properties: {
					LogGroupName: "Fn::Sub": "/aws/lambda/cwsyn-\(cfnStackName)"
					RetentionInDays: 14
				}
			}
			CanaryPolicy: {
				Type: "AWS::IAM::Policy"
				Properties: {
					PolicyDocument: Statement: [
						{
							Effect: "Allow"
							Action: [
								"logs:CreateLogStream",
								"logs:PutLogEvents",
							]
							Resource: "Fn::GetAtt": [
								"CanaryLogGroup",
								"Arn",
							]
						},
					]
					PolicyName: "logs"
					Roles: [
						{
							Ref: "CanaryRole"
						},
					]
				}
			}
			for canary in canaries {
				"Canary\(canary.slug)": {
					Type: "AWS::Synthetics::Canary"
					Properties: {
						ArtifactS3Location: "Fn::Sub": "s3://${CanaryBucket}"
						Code: {
							#handler: #lambdaHandler & {
								url:              canary.url
								expectedHTTPCode: canary.expectedHTTPCode
							}
							Handler: "index.handler"
							Script:  #handler.script
						}
						ExecutionRoleArn: "Fn::GetAtt": [
							"CanaryRole",
							"Arn",
						]
						FailureRetentionPeriod: 30
						Name:                   canary.name
						RunConfig: TimeoutInSeconds: canary.timeoutInSeconds
						RuntimeVersion: "syn-1.0"
						Schedule: {
							DurationInSeconds: "0"
							Expression:        "rate(\(canary.intervalExpression))"
						}
						StartCanaryAfterCreation: true
						SuccessRetentionPeriod:   30
					}
				}
				"SuccessPercentAlarm\(canary.slug)": {
					DependsOn: "TopicPolicy"
					Type:      "AWS::CloudWatch::Alarm"
					Properties: {
						AlarmActions: [
							{
								Ref: "Topic"
							},
						]
						AlarmDescription:   "Canary is failing."
						ComparisonOperator: "LessThanThreshold"
						Dimensions: [
							{
								Name: "CanaryName"
								Value: Ref: "Canary\(canary.slug)"
							},
						]
						EvaluationPeriods: 1
						MetricName:        "SuccessPercent"
						Namespace:         "CloudWatchSynthetics"
						OKActions: [
							{
								Ref: "Topic"
							},
						]
						Period:           300
						Statistic:        "Minimum"
						Threshold:        90
						TreatMissingData: "notBreaching"
					}
				}
			}
		}
		Outputs: {
			for canary in canaries {
				"\(canary.slug)Canary": Value: Ref: "Canary\(canary.slug)"
				"\(canary.slug)URL": Value: canary.url
			}
			NumberCanaries: Value: len(canaries)
		}
	}
}
