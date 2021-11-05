package dagger

// Execute a command in a container
#Exec: {
	// Reserved for runtime use
	_execID: string

	// Base filesystem copied before command execution. Changes are preserved.
	fs: #FS

	command: #Command
       // Exit code (filled after execution)
       exit: int

       // Optionally attach to command standard input stream
       stdin?: #Stream

       // Optionally attach to command standard output stream
       stdout?: #Stream

       // Optionally attach to command standard error stream
       stderr?: #Stream
}


#Command: {
       // Command to execute
       args: [...string] | string

       // Environment variables
       environ: [string]: string

       // Working directory
       workdir: string | *"/"
}
