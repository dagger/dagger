package main

#CFNTemplate: eksControlPlane: {
	AWSTemplateFormatVersion: "2010-09-09"
	Description:              "Amazon EKS Sample VPC - Private and Public subnets"
	Parameters: {
		VpcBlock: {
			Type:        "String"
			Default:     "192.168.0.0/16"
			Description: "The CIDR range for the VPC. This should be a valid private (RFC 1918) CIDR range."
		}
		PublicSubnet01Block: {
			Type:        "String"
			Default:     "192.168.0.0/18"
			Description: "CidrBlock for public subnet 01 within the VPC"
		}
		PublicSubnet02Block: {
			Type:        "String"
			Default:     "192.168.64.0/18"
			Description: "CidrBlock for public subnet 02 within the VPC"
		}
		PrivateSubnet01Block: {
			Type:        "String"
			Default:     "192.168.128.0/18"
			Description: "CidrBlock for private subnet 01 within the VPC"
		}
		PrivateSubnet02Block: {
			Type:        "String"
			Default:     "192.168.192.0/18"
			Description: "CidrBlock for private subnet 02 within the VPC"
		}
		ClusterName: {
			Type:        "String"
			Description: "The EKS cluster name"
		}
	}
	Metadata: "AWS::CloudFormation::Interface": ParameterGroups: [
		{
			Label: default: "Worker Network Configuration"
			Parameters: [
				"VpcBlock",
				"PublicSubnet01Block",
				"PublicSubnet02Block",
				"PrivateSubnet01Block",
				"PrivateSubnet02Block",
			]
		},
	]
	Resources: {
		VPC: {
			Type: "AWS::EC2::VPC"
			Properties: {
				CidrBlock: Ref: "VpcBlock"
				EnableDnsSupport:   true
				EnableDnsHostnames: true
				Tags: [
					{
						Key: "Name"
						Value: "Fn::Sub": "${AWS::StackName}-VPC"
					},
				]
			}
		}
		InternetGateway: Type: "AWS::EC2::InternetGateway"
		VPCGatewayAttachment: {
			Type: "AWS::EC2::VPCGatewayAttachment"
			Properties: {
				InternetGatewayId: Ref: "InternetGateway"
				VpcId: Ref:             "VPC"
			}
		}
		PublicRouteTable: {
			Type: "AWS::EC2::RouteTable"
			Properties: {
				VpcId: Ref: "VPC"
				Tags: [
					{
						Key:   "Name"
						Value: "Public Subnets"
					},
					{
						Key:   "Network"
						Value: "Public"
					},
				]
			}
		}
		PrivateRouteTable01: {
			Type: "AWS::EC2::RouteTable"
			Properties: {
				VpcId: Ref: "VPC"
				Tags: [
					{
						Key:   "Name"
						Value: "Private Subnet AZ1"
					},
					{
						Key:   "Network"
						Value: "Private01"
					},
				]
			}
		}
		PrivateRouteTable02: {
			Type: "AWS::EC2::RouteTable"
			Properties: {
				VpcId: Ref: "VPC"
				Tags: [
					{
						Key:   "Name"
						Value: "Private Subnet AZ2"
					},
					{
						Key:   "Network"
						Value: "Private02"
					},
				]
			}
		}
		PublicRoute: {
			DependsOn: "VPCGatewayAttachment"
			Type:      "AWS::EC2::Route"
			Properties: {
				RouteTableId: Ref: "PublicRouteTable"
				DestinationCidrBlock: "0.0.0.0/0"
				GatewayId: Ref: "InternetGateway"
			}
		}
		PrivateRoute01: {
			DependsOn: [
				"VPCGatewayAttachment",
				"NatGateway01",
			]
			Type: "AWS::EC2::Route"
			Properties: {
				RouteTableId: Ref: "PrivateRouteTable01"
				DestinationCidrBlock: "0.0.0.0/0"
				NatGatewayId: Ref: "NatGateway01"
			}
		}
		PrivateRoute02: {
			DependsOn: [
				"VPCGatewayAttachment",
				"NatGateway02",
			]
			Type: "AWS::EC2::Route"
			Properties: {
				RouteTableId: Ref: "PrivateRouteTable02"
				DestinationCidrBlock: "0.0.0.0/0"
				NatGatewayId: Ref: "NatGateway02"
			}
		}
		NatGateway01: {
			DependsOn: [
				"NatGatewayEIP1",
				"PublicSubnet01",
				"VPCGatewayAttachment",
			]
			Type: "AWS::EC2::NatGateway"
			Properties: {
				AllocationId: "Fn::GetAtt": [
					"NatGatewayEIP1",
					"AllocationId",
				]
				SubnetId: Ref: "PublicSubnet01"
				Tags: [
					{
						Key: "Name"
						Value: "Fn::Sub": "${AWS::StackName}-NatGatewayAZ1"
					},
				]
			}
		}
		NatGateway02: {
			DependsOn: [
				"NatGatewayEIP2",
				"PublicSubnet02",
				"VPCGatewayAttachment",
			]
			Type: "AWS::EC2::NatGateway"
			Properties: {
				AllocationId: "Fn::GetAtt": [
					"NatGatewayEIP2",
					"AllocationId",
				]
				SubnetId: Ref: "PublicSubnet02"
				Tags: [
					{
						Key: "Name"
						Value: "Fn::Sub": "${AWS::StackName}-NatGatewayAZ2"
					},
				]
			}
		}
		NatGatewayEIP1: {
			DependsOn: [
				"VPCGatewayAttachment",
			]
			Type: "AWS::EC2::EIP"
			Properties: Domain: "vpc"
		}
		NatGatewayEIP2: {
			DependsOn: [
				"VPCGatewayAttachment",
			]
			Type: "AWS::EC2::EIP"
			Properties: Domain: "vpc"
		}
		PublicSubnet01: {
			Type: "AWS::EC2::Subnet"
			Metadata: Comment: "Subnet 01"
			Properties: {
				MapPublicIpOnLaunch: true
				AvailabilityZone: "Fn::Select": [
					"0",
					{
						"Fn::GetAZs": Ref: "AWS::Region"
					},
				]
				CidrBlock: Ref: "PublicSubnet01Block"
				VpcId: Ref:     "VPC"
				Tags: [
					{
						Key: "Name"
						Value: "Fn::Sub": "${AWS::StackName}-PublicSubnet01"
					},
					{
						Key: "Fn::Sub": "kubernetes.io/cluster/${ClusterName}"
						Value: "shared"
					},
				]
			}
		}
		PublicSubnet02: {
			Type: "AWS::EC2::Subnet"
			Metadata: Comment: "Subnet 02"
			Properties: {
				MapPublicIpOnLaunch: true
				AvailabilityZone: "Fn::Select": [
					"1",
					{
						"Fn::GetAZs": Ref: "AWS::Region"
					},
				]
				CidrBlock: Ref: "PublicSubnet02Block"
				VpcId: Ref:     "VPC"
				Tags: [
					{
						Key: "Name"
						Value: "Fn::Sub": "${AWS::StackName}-PublicSubnet02"
					},
					{
						Key: "Fn::Sub": "kubernetes.io/cluster/${ClusterName}"
						Value: "shared"
					},
				]
			}
		}
		PrivateSubnet01: {
			Type: "AWS::EC2::Subnet"
			Metadata: Comment: "Subnet 03"
			Properties: {
				AvailabilityZone: "Fn::Select": [
					"0",
					{
						"Fn::GetAZs": Ref: "AWS::Region"
					},
				]
				CidrBlock: Ref: "PrivateSubnet01Block"
				VpcId: Ref:     "VPC"
				Tags: [
					{
						Key: "Name"
						Value: "Fn::Sub": "${AWS::StackName}-PrivateSubnet01"
					},
					{
						Key: "Fn::Sub": "kubernetes.io/cluster/${ClusterName}"
						Value: "shared"
					},
				]
			}
		}
		PrivateSubnet02: {
			Type: "AWS::EC2::Subnet"
			Metadata: Comment: "Private Subnet 02"
			Properties: {
				AvailabilityZone: "Fn::Select": [
					"1",
					{
						"Fn::GetAZs": Ref: "AWS::Region"
					},
				]
				CidrBlock: Ref: "PrivateSubnet02Block"
				VpcId: Ref:     "VPC"
				Tags: [
					{
						Key: "Name"
						Value: "Fn::Sub": "${AWS::StackName}-PrivateSubnet02"
					},
					{
						Key: "Fn::Sub": "kubernetes.io/cluster/${ClusterName}"
						Value: "shared"
					},
				]
			}
		}
		PublicSubnet01RouteTableAssociation: {
			Type: "AWS::EC2::SubnetRouteTableAssociation"
			Properties: {
				SubnetId: Ref:     "PublicSubnet01"
				RouteTableId: Ref: "PublicRouteTable"
			}
		}
		PublicSubnet02RouteTableAssociation: {
			Type: "AWS::EC2::SubnetRouteTableAssociation"
			Properties: {
				SubnetId: Ref:     "PublicSubnet02"
				RouteTableId: Ref: "PublicRouteTable"
			}
		}
		PrivateSubnet01RouteTableAssociation: {
			Type: "AWS::EC2::SubnetRouteTableAssociation"
			Properties: {
				SubnetId: Ref:     "PrivateSubnet01"
				RouteTableId: Ref: "PrivateRouteTable01"
			}
		}
		PrivateSubnet02RouteTableAssociation: {
			Type: "AWS::EC2::SubnetRouteTableAssociation"
			Properties: {
				SubnetId: Ref:     "PrivateSubnet02"
				RouteTableId: Ref: "PrivateRouteTable02"
			}
		}
		ControlPlaneSecurityGroup: {
			Type: "AWS::EC2::SecurityGroup"
			Properties: {
				GroupDescription: "Cluster communication with worker nodes"
				VpcId: Ref: "VPC"
			}
		}
		EKSIAMRole: {
			Type: "AWS::IAM::Role"
			Properties: {
				AssumeRolePolicyDocument: Statement: [
					{
						Effect: "Allow"
						Principal: Service: [
							"eks.amazonaws.com",
						]
						Action: [
							"sts:AssumeRole",
						]

					},
				]
				ManagedPolicyArns: [
					"arn:aws:iam::aws:policy/AmazonEKSClusterPolicy",
					"arn:aws:iam::aws:policy/AmazonEKSServicePolicy",
				]
			}
		}
		EKSCluster: {
			Type: "AWS::EKS::Cluster"
			Properties: {
				Name: Ref: "ClusterName"
				Version: "1.19"
				RoleArn: "Fn::GetAtt": ["EKSIAMRole", "Arn"]
				ResourcesVpcConfig: {
					SecurityGroupIds: [{Ref: "ControlPlaneSecurityGroup"}]
					SubnetIds: [
						{Ref: "PublicSubnet01"},
						{Ref: "PublicSubnet02"},
						{Ref: "PrivateSubnet01"},
						{Ref: "PrivateSubnet02"},
					]
				}
			}
			DependsOn: ["EKSIAMRole", "PublicSubnet01", "PublicSubnet02", "PrivateSubnet01", "PrivateSubnet02", "ControlPlaneSecurityGroup"]
		}
	}
	Outputs: {
		SubnetIds: {
			Description: "Subnets IDs in the VPC"
			Value: "Fn::Join": [
				",",
				[
					{
						Ref: "PublicSubnet01"
					},
					{
						Ref: "PublicSubnet02"
					},
					{
						Ref: "PrivateSubnet01"
					},
					{
						Ref: "PrivateSubnet02"
					},
				],
			]
		}
		PublicSubnets: {
			Description: "List of the public subnets"
			Value: "Fn::Join": [
				",",
				[
					{
						Ref: "PublicSubnet01"
					},
					{
						Ref: "PublicSubnet02"
					},
				],
			]
		}
		PrivateSubnets: {
			Description: "List of the private subnets"
			Value: "Fn::Join": [
				",",
				[
					{
						Ref: "PrivateSubnet01"
					},
					{
						Ref: "PrivateSubnet02"
					},
				],
			]
		}
		DefaultSecurityGroup: {
			Description: "Security group for the cluster control plane communication with worker nodes"
			Value: "Fn::Join": [
				",",
				[
					{
						Ref: "ControlPlaneSecurityGroup"
					},
				],
			]
		}
		VPC: {
			Description: "The VPC Id"
			Value: Ref: "VPC"
		}
	}
}
