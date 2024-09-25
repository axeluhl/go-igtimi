# Changelog for lib_protobuf

## Build X - XXXX/XX/XX

##### Fixed
- Changed the name of 'Object' in YachtBotSessionLogEntry to 'Entity'

## Build 2 - 2019/05/17

#### IgtimiData.proto - Version 0.2.0

##### Added
- Session log entry action, ACTION_UPDATE

##### Fixed
- Changed short name of Power from 'pwd' to 'pwr'

## Build 1 - 2019/05/14

### Igtimi

#### IgtimiData.proto - Version 0.1.0

##### Added
- All of the Igtimi supported data types

#### IgtimiStream.proto - Version 0.1.0

##### Added
- Protocol for connecting / controlling streams to Igtimi RIoT nodes.

#### IgtimiTimeSync.proto - Version 0.1.0

##### Added
- Definitions for performing basic SNTP time sync with Igtimi RIoT nodes.

### YachtBot

#### YachtBotSessionLogEntry.proto - Version 0.1.0

##### Added
- Definition of the session log entry schema as defined by the YachtBot application.

#### YachtBotEvents.proto - Version 0.1.0

##### Added
- Schema for data stored in the events table by a YachtBot application.

### Internal-Igtimi

#### IgtimiLogging.proto - Version 0.1.0

##### Added
- Scheme used for accessing logs for Igtimi systems remotely.
