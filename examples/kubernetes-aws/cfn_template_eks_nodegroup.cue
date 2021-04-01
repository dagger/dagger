package main

#CFNTemplate: eksNodeGroup: {
	AWSTemplateFormatVersion: "2010-09-09"
	Description:              "Amazon EKS - Node Group"
	Parameters: {
		ClusterName: {
			Type:        "String"
			Description: "The cluster name provided when the cluster was created. If it is incorrect, nodes will not be able to join the cluster."
		}
		NodeAutoScalingGroupDesiredCapacity: {
			Type:        "Number"
			Default:     3
			Description: "Desired capacity of Node Group ASG."
		}
		NodeAutoScalingGroupMaxSize: {
			Type:        "Number"
			Default:     4
			Description: "Maximum size of Node Group ASG. Set to at least 1 greater than NodeAutoScalingGroupDesiredCapacity."
		}
		NodeAutoScalingGroupMinSize: {
			Type:        "Number"
			Default:     1
			Description: "Minimum size of Node Group ASG."
		}
		NodeInstanceType: {
			Type:                  "String"
			Default:               "t3.medium"
			ConstraintDescription: "Must be a valid EC2 instance type"
			Description:           "EC2 instance type for the node instances"
		}
		Subnets: {
			Type:        "List<AWS::EC2::Subnet::Id>"
			Description: "The subnets where workers can be created."
		}
	}
	Resources: {
		NodeInstanceRole: {
			Type: "AWS::IAM::Role"
			Properties: {
				AssumeRolePolicyDocument: {
					Version: "2012-10-17"
					Statement: [
						{
							Effect: "Allow"
							Principal: Service: [
								"ec2.amazonaws.com",
							]
							Action: [
								"sts:AssumeRole",
							]
						},
					]
				}
				ManagedPolicyArns: [
					"arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
					"arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
					"arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
				]
				Path: "/"
			}
		}
		Nodegroup: {
			Type: "AWS::EKS::Nodegroup"
			Properties: {
				ClusterName: Ref: "ClusterName"
				NodeRole: "Fn::GetAtt": [
					"NodeInstanceRole",
					"Arn",
				]
				ScalingConfig: {
					MaxSize: Ref:     "NodeAutoScalingGroupMaxSize"
					MinSize: Ref:     "NodeAutoScalingGroupMinSize"
					DesiredSize: Ref: "NodeAutoScalingGroupDesiredCapacity"
				}
				InstanceTypes: [{Ref: "NodeInstanceType"}]
				AmiType: "AL2_x86_64"
				Subnets: Ref: "Subnets"
			}
		}
	}
	Outputs: NodeInstanceRole: {
		Description: "The node instance role"
		Value: "Fn::GetAtt": [
			"NodeInstanceRole",
			"Arn",
		]
	}
}
