export const ShellTabs = ({ systemShellCommand, daggerShellCommand, daggerCliCommand }) => {
  const [preferredShell, setPreferredShell] = useState('system');

  // Load preferred shell from localStorage on component mount
  useEffect(() => {
    const savedShell = localStorage.getItem('preferredShell');
    if (savedShell) {
      setPreferredShell(savedShell);
    }
  }, []);

  // Save preferred shell to localStorage when it changes
  useEffect(() => {
    localStorage.setItem('preferredShell', preferredShell);
  }, [preferredShell]);

  const handleShellChange = (shell) => {
    setPreferredShell(shell);
  };

  const renderCommand = (command) => {
    return (
      <pre>
        <code>{command}</code>
      </pre>
    );
  };

  return (
    <div>
      <Tabs>
        <Tab title="System Shell">{renderCommand(systemShellCommand)}</Tab>
        <Tab title="Dagger Shell">{renderCommand(daggerShellCommand)}</Tab>
        <Tab title="Dagger CLI">{renderCommand(daggerCliCommand)}</Tab>
      </Tabs>
    </div>
  );
};

// For backward compatibility with the CodeGroup usage
export default function ({ children }) {
  // Parse commands from children if using the old format
  // This is a simplified version - you might need to adjust based on actual children structure
  const commands = {
    systemShellCommand: '{systemShellCommand}',
    daggerShellCommand: '{daggerShellCommand}',
    daggerCliCommand: '{daggerCliCommand}'
  };

  return <ShellTabs {...commands} />;
}