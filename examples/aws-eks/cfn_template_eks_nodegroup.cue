package main

#CFNTemplate: eksNodeGroup: {
	AWSTemplateFormatVersion: "2010-09-09"
	Description:              "Amazon EKS - Node Group"
	Metadata: "AWS::CloudFormation::Interface": ParameterGroups: [
		{
			Label: default: "EKS Cluster"
			Parameters: [
				"ClusterName",
				"ClusterControlPlaneSecurityGroup",
			]
		},
		{
			Label: default: "Worker Node Configuration"
			Parameters: [
				"NodeGroupName",
				"NodeAutoScalingGroupMinSize",
				"NodeAutoScalingGroupDesiredCapacity",
				"NodeAutoScalingGroupMaxSize",
				"NodeInstanceType",
				"NodeImageIdSSMParam",
				"NodeImageId",
				"NodeVolumeSize",
				// "KeyName",
				"BootstrapArguments",
			]
		},
		{
			Label: default: "Worker Network Configuration"
			Parameters: [
				"VpcId",
				"Subnets",
			]
		},
	]
	Parameters: {
		BootstrapArguments: {
			Type:        "String"
			Default:     ""
			Description: "Arguments to pass to the bootstrap script. See files/bootstrap.sh in https://github.com/awslabs/amazon-eks-ami"
		}
		ClusterControlPlaneSecurityGroup: {
			Type:        "AWS::EC2::SecurityGroup::Id"
			Description: "The security group of the cluster control plane."
		}
		ClusterName: {
			Type:        "String"
			Description: "The cluster name provided when the cluster was created. If it is incorrect, nodes will not be able to join the cluster."
		}
		// KeyName: {
		//  Type:        "AWS::EC2::KeyPair::KeyName"
		//  Description: "The EC2 Key Pair to allow SSH access to the instances"
		// }
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
		NodeGroupName: {
			Type:        "String"
			Description: "Unique identifier for the Node Group."
		}
		NodeImageId: {
			Type:        "String"
			Default:     ""
			Description: "(Optional) Specify your own custom image ID. This value overrides any AWS Systems Manager Parameter Store value specified above."
		}
		NodeImageIdSSMParam: {
			Type:        "AWS::SSM::Parameter::Value<AWS::EC2::Image::Id>"
			Default:     "/aws/service/eks/optimized-ami/1.19/amazon-linux-2/recommended/image_id"
			Description: "AWS Systems Manager Parameter Store parameter of the AMI ID for the worker node instances."
		}
		NodeInstanceType: {
			Type:                  "String"
			Default:               "t3.medium"
			ConstraintDescription: "Must be a valid EC2 instance type"
			Description:           "EC2 instance type for the node instances"
		}
		NodeVolumeSize: {
			Type:        "Number"
			Default:     20
			Description: "Node volume size"
		}
		Subnets: {
			Type:        "List<AWS::EC2::Subnet::Id>"
			Description: "The subnets where workers can be created."
		}
		VpcId: {
			Type:        "AWS::EC2::VPC::Id"
			Description: "The VPC of the worker instances"
		}
	}
	Conditions: HasNodeImageId: "Fn::Not": [
		{
			"Fn::Equals": [
				{
					Ref: "NodeImageId"
				},
				"",
			]
		},
	]
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
		NodeInstanceProfile: {
			Type: "AWS::IAM::InstanceProfile"
			Properties: {
				Path: "/"
				Roles: [
					{
						Ref: "NodeInstanceRole"
					},
				]
			}
		}
		NodeSecurityGroup: {
			Type: "AWS::EC2::SecurityGroup"
			Properties: {
				GroupDescription: "Security group for all nodes in the cluster"
				Tags: [
					{
						Key: "Fn::Sub": "kubernetes.io/cluster/${ClusterName}"
						Value: "owned"
					},
				]
				VpcId: Ref: "VpcId"
			}
		}
		NodeSecurityGroupIngress: {
			Type:      "AWS::EC2::SecurityGroupIngress"
			DependsOn: "NodeSecurityGroup"
			Properties: {
				Description: "Allow node to communicate with each other"
				FromPort:    0
				GroupId: Ref: "NodeSecurityGroup"
				IpProtocol: "-1"
				SourceSecurityGroupId: Ref: "NodeSecurityGroup"
				ToPort: 65535
			}
		}
		ClusterControlPlaneSecurityGroupIngress: {
			Type:      "AWS::EC2::SecurityGroupIngress"
			DependsOn: "NodeSecurityGroup"
			Properties: {
				Description: "Allow pods to communicate with the cluster API Server"
				FromPort:    443
				GroupId: Ref: "ClusterControlPlaneSecurityGroup"
				IpProtocol: "tcp"
				SourceSecurityGroupId: Ref: "NodeSecurityGroup"
				ToPort: 443
			}
		}
		ControlPlaneEgressToNodeSecurityGroup: {
			Type:      "AWS::EC2::SecurityGroupEgress"
			DependsOn: "NodeSecurityGroup"
			Properties: {
				Description: "Allow the cluster control plane to communicate with worker Kubelet and pods"
				DestinationSecurityGroupId: Ref: "NodeSecurityGroup"
				FromPort: 1025
				GroupId: Ref: "ClusterControlPlaneSecurityGroup"
				IpProtocol: "tcp"
				ToPort:     65535
			}
		}
		ControlPlaneEgressToNodeSecurityGroupOn443: {
			Type:      "AWS::EC2::SecurityGroupEgress"
			DependsOn: "NodeSecurityGroup"
			Properties: {
				Description: "Allow the cluster control plane to communicate with pods running extension API servers on port 443"
				DestinationSecurityGroupId: Ref: "NodeSecurityGroup"
				FromPort: 443
				GroupId: Ref: "ClusterControlPlaneSecurityGroup"
				IpProtocol: "tcp"
				ToPort:     443
			}
		}
		NodeSecurityGroupFromControlPlaneIngress: {
			Type:      "AWS::EC2::SecurityGroupIngress"
			DependsOn: "NodeSecurityGroup"
			Properties: {
				Description: "Allow worker Kubelets and pods to receive communication from the cluster control plane"
				FromPort:    1025
				GroupId: Ref: "NodeSecurityGroup"
				IpProtocol: "tcp"
				SourceSecurityGroupId: Ref: "ClusterControlPlaneSecurityGroup"
				ToPort: 65535
			}
		}
		NodeSecurityGroupFromControlPlaneOn443Ingress: {
			Type:      "AWS::EC2::SecurityGroupIngress"
			DependsOn: "NodeSecurityGroup"
			Properties: {
				Description: "Allow pods running extension API servers on port 443 to receive communication from cluster control plane"
				FromPort:    443
				GroupId: Ref: "NodeSecurityGroup"
				IpProtocol: "tcp"
				SourceSecurityGroupId: Ref: "ClusterControlPlaneSecurityGroup"
				ToPort: 443
			}
		}
		NodeLaunchConfig: {
			Type: "AWS::AutoScaling::LaunchConfiguration"
			Properties: {
				AssociatePublicIpAddress: "true"
				BlockDeviceMappings: [
					{
						DeviceName: "/dev/xvda"
						Ebs: {
							DeleteOnTermination: true
							VolumeSize: Ref: "NodeVolumeSize"
							VolumeType: "gp2"
						}
					},
				]
				IamInstanceProfile: Ref: "NodeInstanceProfile"
				ImageId: "Fn::If": [
					"HasNodeImageId",
					{
						Ref: "NodeImageId"
					},
					{
						Ref: "NodeImageIdSSMParam"
					},
				]
				InstanceType: Ref: "NodeInstanceType"
				// KeyName: Ref:      "KeyName"
				SecurityGroups: [
					{
						Ref: "NodeSecurityGroup"
					},
				]
				UserData: "Fn::Base64": "Fn::Sub": "#!/bin/bash\nset -o xtrace\n/etc/eks/bootstrap.sh ${ClusterName} ${BootstrapArguments}\n/opt/aws/bin/cfn-signal --exit-code $? \\\n         --stack  ${AWS::StackName} \\\n         --resource NodeGroup  \\\n         --region ${AWS::Region}\n"
			}
		}
		NodeGroup: {
			Type: "AWS::AutoScaling::AutoScalingGroup"
			Properties: {
				DesiredCapacity: Ref:         "NodeAutoScalingGroupDesiredCapacity"
				LaunchConfigurationName: Ref: "NodeLaunchConfig"
				MaxSize: Ref:                 "NodeAutoScalingGroupMaxSize"
				MinSize: Ref:                 "NodeAutoScalingGroupMinSize"
				Tags: [
					{
						Key:               "Name"
						PropagateAtLaunch: "true"
						Value: "Fn::Sub": "${ClusterName}-${NodeGroupName}-Node"
					},
					{
						Key: "Fn::Sub": "kubernetes.io/cluster/${ClusterName}"
						PropagateAtLaunch: "true"
						Value:             "owned"
					},
				]
				VPCZoneIdentifier: Ref: "Subnets"
			}
			UpdatePolicy: AutoScalingRollingUpdate: {
				MaxBatchSize: "1"
				MinInstancesInService: Ref: "NodeAutoScalingGroupDesiredCapacity"
				PauseTime: "PT5M"
			}
		}
	}
	Outputs: {
		NodeInstanceRole: {
			Description: "The node instance role"
			Value: "Fn::GetAtt": [
				"NodeInstanceRole",
				"Arn",
			]
		}
		NodeSecurityGroup: {
			Description: "The security group for the node group"
			Value: Ref: "NodeSecurityGroup"
		}
	}
}
