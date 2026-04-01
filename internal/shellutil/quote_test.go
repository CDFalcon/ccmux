package shellutil

import "testing"

func TestQuote_should_wrap_simple_string_given_no_special_chars(t *testing.T) {
	// Setup.
	input := "hello world"

	// Execute.
	result := Quote(input)

	// Assert.
	if result != "'hello world'" {
		t.Errorf("got %s, want 'hello world'", result)
	}
}

func TestQuote_should_escape_backticks_given_backtick_input(t *testing.T) {
	// Setup.
	input := "run `ls` now"

	// Execute.
	result := Quote(input)

	// Assert.
	if result != "'run `ls` now'" {
		t.Errorf("got %s, want 'run `ls` now'", result)
	}
}

func TestQuote_should_escape_dollar_given_variable_expansion(t *testing.T) {
	// Setup.
	input := "hello $HOME"

	// Execute.
	result := Quote(input)

	// Assert.
	if result != "'hello $HOME'" {
		t.Errorf("got %s, want 'hello $HOME'", result)
	}
}

func TestQuote_should_escape_dollar_paren_given_command_substitution(t *testing.T) {
	// Setup.
	input := "run $(whoami)"

	// Execute.
	result := Quote(input)

	// Assert.
	if result != "'run $(whoami)'" {
		t.Errorf("got %s, want 'run $(whoami)'", result)
	}
}

func TestQuote_should_escape_single_quotes_given_embedded_single_quotes(t *testing.T) {
	// Setup.
	input := "it's a test"

	// Execute.
	result := Quote(input)

	// Assert.
	expected := "'it'\\''s a test'"
	if result != expected {
		t.Errorf("got %s, want %s", result, expected)
	}
}

func TestQuote_should_handle_double_quotes_given_embedded_double_quotes(t *testing.T) {
	// Setup.
	input := `say "hello"`

	// Execute.
	result := Quote(input)

	// Assert.
	expected := `'say "hello"'`
	if result != expected {
		t.Errorf("got %s, want %s", result, expected)
	}
}

func TestQuote_should_handle_empty_string_given_empty_input(t *testing.T) {
	// Setup.
	input := ""

	// Execute.
	result := Quote(input)

	// Assert.
	if result != "''" {
		t.Errorf("got %s, want ''", result)
	}
}

func TestQuote_should_handle_backslashes_given_escaped_chars(t *testing.T) {
	// Setup.
	input := `path\to\file`

	// Execute.
	result := Quote(input)

	// Assert.
	expected := `'path\to\file'`
	if result != expected {
		t.Errorf("got %s, want %s", result, expected)
	}
}
