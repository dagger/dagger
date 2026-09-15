ExUnit.start()

mneme_opts =
  if System.get_env("MNEME_ACCEPT") do
    [action: :accept, default_pattern: :last]
  else
    []
  end

Mneme.start(mneme_opts)
