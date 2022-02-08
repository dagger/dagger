@if(fullop)

package op

// Full resolution schema enforcing the complete op spec
#Op: (#Export |
	#FetchContainer |
	#PushContainer |
	#FetchGit |
	#FetchHTTP |
	#Exec |
	#Local |
	#Copy |
	#Load |
	#Subdir |
	#Workdir |
	#WriteFile |
	#Mkdir |
	#DockerBuild) & {do: string}
