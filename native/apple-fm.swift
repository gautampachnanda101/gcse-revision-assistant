import Foundation
import FoundationModels

struct ChatMessage: Codable {
    let role: String
    let content: String
}

struct ChatRequest: Codable {
    let messages: [ChatMessage]
}

struct ChatResponse: Codable {
    let model: String
    let content: String
}

enum BridgeError: Error {
    case modelUnavailable(String)
}

@main
struct AppleFoundationModelsBridge {
    static func main() async {
        let model = SystemLanguageModel.default
        if CommandLine.arguments.contains("--availability") {
            print(model.isAvailable ? "available" : String(describing: model.availability))
            if !model.isAvailable { Foundation.exit(1) }
            return
        }
        guard case .available = model.availability else {
            let reason = String(describing: model.availability)
            writeError("Apple Intelligence is not available: \(reason)")
            return
        }
        do {
            let input = FileHandle.standardInput.readDataToEndOfFile()
            let request = try JSONDecoder().decode(ChatRequest.self, from: input)
            let system = request.messages.first(where: { $0.role == "system" })?.content ?? ""
            let conversation = request.messages
                .filter { $0.role != "system" }
                .map { "\($0.role.capitalized): \($0.content)" }
                .joined(separator: "\n\n")
            let session = LanguageModelSession(instructions: system)
            let response = try await session.respond(to: conversation)
            let payload = try JSONEncoder().encode(ChatResponse(model: "Apple Foundation Models", content: response.content))
            FileHandle.standardOutput.write(payload)
        } catch {
            writeError(String(describing: error))
        }
    }

    static func writeError(_ message: String) {
        let data = try? JSONSerialization.data(withJSONObject: ["error": message])
        FileHandle.standardError.write(data ?? Data())
        Foundation.exit(1)
    }
}
