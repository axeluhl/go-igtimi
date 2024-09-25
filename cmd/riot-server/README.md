## riot-server-demo

Example server for the riot protocol. Can be used as the base for a custom server implementation.

### Building

```bash
buf generate
CGO_ENABLED=0 go build ./cmd/riot-server
```

### Running
The server needs to be run


**Device Configuration**
Yachtbot/Windbot devices need to run at least 2.2.540 to be able to connect to a custom server.

Add the following to the device configuration:
```
riot server_address <address> <port>
```
The address can be an IPv4 address or a domain name. The port is the port the server is running on.


### Protocol
The devices send messages according to the riot protocol, as defined in [/proto/com/igtimi](/proto/com/igtimi/). The protocol is based on the [protobuf](https://developers.google.com/protocol-buffers) serialization format. Devices connect to the server via a TCP connection.

#### Acknowledgments
Devices send all messages to the server without prompting. No acknowledgement is needed from the server.
The server only has to send a heartbeat message to the devices every 15 seconds. The heartbeat message is defined in the protocol.
If the device does not receive a heartbeat message within 30 seconds, it will disconnect and attempt to reconnect.

#### Authentication
The server may authenticate devices based on the so called device group token.
The token is a string that is sent initially after connection establishment.

#### Commands
The server may send commands to the devices using the [*com.igtimi.DeviceCommand*](/proto/com/igtimi/IgtimiDevice.proto) message.