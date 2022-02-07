# rcon
A go written library for the [RCON Protocol](https://developer.valvesoftware.com/wiki/Source_RCON_Protocol).

This is a fork from [james4k/rcon](https://github.com/james4k/rcon) with the support for go modules and with a rework of the original implementation for better readability.

## Usage
```golang
// Establish a connection.
remoteConsole, err := rcon.Dial("127.0.0.1", "password")
if err != nil {
    log.Fatal(err)
}
// Close the connection at the end to free the used resources.
defer remoteConsole.Close()

// Send a command.
requestID, err := remoteConsole.Write("command")
if err != nil {
    log.Fatal(err)
}

// Read the response.
response, responseID, err := remoteConsole.Read()
if err != nil {
    log.Fatal(err)
}
if requestID != responseID {
    log.Fatal("response id doesn't match the request id!")
}

fmt.Println(response)
```

## License
This lib is licensed under the [MIT License](LICENSE).

## Contributors
If you should encounter a bug or a missing feature don't hesitate to open an issue or even submit a pull-request.


Special thx to [nhh](https://github.com/nhh) and [dnltinney](https://github.com/dnltinney) for the great help debugging this lib.