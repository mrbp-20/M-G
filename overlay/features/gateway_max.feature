Feature: MAX chat turns
  The bot takes a message in a direct chat as an agent turn and answers it,
  never answers itself, runs one turn per message, and takes a few commands of
  its own.

  Background:
    Given a fake MAX Bot API whose bot is "coddy_bot"
    And a max gateway over a scripted agent pointed at it
    And the bot is started

  Scenario: a direct message starts a turn and gets an answer
    When the person "anna" writes "hello" in a direct chat
    Then the direct chat with "anna" shows a bot message "plain answer"
    And the agent was asked "hello"

  Scenario: the bot does not answer its own messages
    When a message from the bot itself arrives in the direct chat with "anna"
    Then the agent was asked nothing

  Scenario: /clear starts a new session
    Given the bot counts "anna" among its admins
    And the person "anna" wrote "hello" in a direct chat and got an answer
    When the person "anna" writes "/clear" in a direct chat
    Then the direct chat with "anna" shows a bot message "Начата новая беседа."
    And the agent was asked nothing more

  Scenario: /model opens a menu and a tap switches the model
    Given the bot counts "anna" among its admins
    When the person "anna" opens the model menu
    Then the direct chat with "anna" shows a keyboard that ticks "openai/gpt-4o"
    When the person "anna" taps the model button "rpa/qwen3.6-35b-a3b"
    Then the session model for "anna" is "rpa/qwen3.6-35b-a3b"
    And the direct chat with "anna" shows an edited keyboard that ticks "rpa/qwen3.6-35b-a3b"

  Scenario: a member can neither clear the conversation nor pick a model
    When the person "anna" writes "/clear" in a direct chat
    Then the direct chat with "anna" shows a bot message "Менять настройки в этом чате могут только админы бота."
    And the person "anna" opens the model menu and is refused
    And the agent was asked nothing

  Scenario: a direct message with a photo reaches the model as an image part
    When the person "anna" sends a photo with text "what is on the picture" in a direct chat
    Then the agent was asked "what is on the picture"
    And the agent received an image part starting with "data:image/png;base64,"

  Scenario: the picture the agent read reaches the chat
    Given the agent will show the picture "shot.png"
    When the person "anna" writes "look at the picture" in a direct chat
    Then the direct chat with "anna" receives a photo captioned "shot.png"
    And the agent was asked "look at the picture"

  Scenario: the bot tells the client which commands it answers
    Then the bot published 11 commands
    And the first published command is "start"

  Scenario: a member is not shown the list of conversations
    When the person "anna" writes "/resume" in a direct chat
    Then the direct chat with "anna" shows a bot message "Менять настройки в этом чате могут только админы бота."

  Scenario: /resume answers the admin when there is nothing to resume
    Given the bot counts "anna" among its admins
    When the person "anna" writes "/resume" in a direct chat
    Then the direct chat with "anna" shows a bot message "Бесед пока нет."

  Scenario: the document the agent produced reaches the chat
    Given the agent will share the document "report.pdf"
    When the person "anna" writes "send me the report" in a direct chat
    Then the direct chat with "anna" receives a document named "report.pdf"
    And the agent was asked "send me the report"
